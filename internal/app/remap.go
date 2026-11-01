package app

import (
	"maps"
	"sort"

	"github.com/patrickdeangelis/pad2go/internal/mapping"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
	"github.com/patrickdeangelis/pad2go/internal/virtualpad"
)

// outputRemap moves one protocol button to another. dst 0 clears the source
// button.
type outputRemap struct {
	src, dst uint32
}

// cloneRemaps copies a scope -> source -> target map so neither the saved
// configuration nor the caller can mutate the running app's copy.
func cloneRemaps(remaps map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(remaps))
	for scope, entries := range remaps {
		out[scope] = maps.Clone(entries)
	}
	return out
}

// compileOutputRemaps merges the global remaps with the selected output's
// overrides. The per-output entry wins for the same source; "Default" leaves
// the inherited mapping alone and "None" clears the button.
func compileOutputRemaps(global, perOutput map[string]string) []outputRemap {
	if len(global) == 0 && len(perOutput) == 0 {
		return nil
	}
	final := make(map[uint32]uint32, len(global)+len(perOutput))
	add := func(remaps map[string]string) {
		for source, target := range remaps {
			src, ok := protocol.ButtonsByName[source]
			if !ok {
				continue
			}
			switch target {
			case "", "Default":
				continue
			case "None":
				final[src] = 0
			default:
				dst, ok := protocol.ButtonsByName[target]
				if !ok {
					continue
				}
				final[src] = dst
			}
		}
	}
	add(global)
	add(perOutput)
	out := make([]outputRemap, 0, len(final))
	for src, dst := range final {
		out = append(out, outputRemap{src: src, dst: dst})
	}
	return out
}

// applyButtonRemaps moves sources onto their targets. Every decision uses the
// original button set, so a target that is also a source does not chain. All
// sources are cleared before any target is added, so a target mapped from one
// button survives another source being disabled with "None".
func applyButtonRemaps(buttons uint32, remaps []outputRemap) uint32 {
	kept := buttons
	for _, rm := range remaps {
		kept &^= rm.src
	}
	for _, rm := range remaps {
		if buttons&rm.src != 0 {
			kept |= rm.dst
		}
	}
	return kept
}

// SetOutputRemaps replaces the remap tables for the running app. The service
// calls it when it rebuilds the runtime, so like the rest of the settings a
// saved change takes effect after Restart.
func (a *App) SetOutputRemaps(remaps map[string]map[string]string) {
	clone := cloneRemaps(remaps)
	a.outputMu.Lock()
	a.outputRemaps = clone
	a.outputMu.Unlock()
}

// outputRemapsFor returns the compiled remaps for one effective output.
func (a *App) outputRemapsFor(output string) []outputRemap {
	a.outputMu.Lock()
	global := maps.Clone(a.outputRemaps["global"])
	perOutput := maps.Clone(a.outputRemaps[output])
	a.outputMu.Unlock()
	return compileOutputRemaps(global, perOutput)
}

// remapOutput is the output whose remap table applies to this slot. It is the
// pad actually created, or, when no backend is available, the output the slot
// asked for so remaps still show up in the input test and DSU. An explicit
// motion-only choice keeps remaps off.
func (s *slot) remapOutput(kind protocol.Kind) string {
	if s.outputKind != "" && s.outputKind != virtualpad.OutputNone {
		return s.outputKind
	}
	if s.outputOverride != "" {
		return s.outputOverride
	}
	return ResolveDefault(s.app.DefaultOutput(), kind)
}

// ResolveDefault is the output a controller of this kind gets from the
// default output: Automática sends a DualSense to DualSense and everything
// else to Xbox 360.
func ResolveDefault(def string, kind protocol.Kind) string {
	switch def {
	case virtualpad.OutputNone:
		return virtualpad.OutputNone
	case virtualpad.OutputAuto, "":
		if kind == protocol.KindDualSense {
			return virtualpad.OutputDualSense
		}
		return virtualpad.OutputXbox
	default:
		return def
	}
}

// Remap is one running output remap, as protocol button names.
type Remap struct {
	Source, Target string
}

// activeRemaps lists the remaps that apply to output after merging the
// global table with the output's own, with the same rules as
// compileOutputRemaps. Unknown names are skipped; "None" stays "None". The
// list is ordered by source button bit.
func activeRemaps(global, perOutput map[string]string) []Remap {
	final := map[string]string{}
	add := func(remaps map[string]string) {
		for source, target := range remaps {
			if _, ok := protocol.ButtonsByName[source]; !ok {
				continue
			}
			switch target {
			case "", "Default":
				continue
			case "None":
			default:
				if _, ok := protocol.ButtonsByName[target]; !ok {
					continue
				}
			}
			final[source] = target
		}
	}
	add(global)
	add(perOutput)
	out := make([]Remap, 0, len(final))
	for source, target := range final {
		out = append(out, Remap{Source: source, Target: target})
	}
	sort.Slice(out, func(i, j int) bool {
		bi, bj := protocol.ButtonsByName[out[i].Source], protocol.ButtonsByName[out[j].Source]
		if bi != bj {
			return bi < bj
		}
		return out[i].Source < out[j].Source
	})
	return out
}

// activeRemapsFor is the running remap list for one output.
func (a *App) activeRemapsFor(output string) []Remap {
	if output == "" || output == virtualpad.OutputNone {
		return nil
	}
	a.outputMu.Lock()
	global := maps.Clone(a.outputRemaps["global"])
	perOutput := maps.Clone(a.outputRemaps[output])
	a.outputMu.Unlock()
	return activeRemaps(global, perOutput)
}

// applyOutputRemaps rewrites the frame's protocol buttons and rebuilds the
// Xbox report from them, so every virtual output and the input test see the
// same remapped result.
func (s *slot) applyOutputRemaps(f mapping.Frame, kind protocol.Kind) mapping.Frame {
	output := s.remapOutput(kind)
	if output == "" || output == virtualpad.OutputNone {
		return f
	}
	remaps := s.app.outputRemapsFor(output)
	if len(remaps) == 0 {
		return f
	}
	remap := func(in protocol.Input) protocol.Input {
		in.Buttons = applyButtonRemaps(in.Buttons, remaps)
		// A button remapped onto a trigger must also drive its analog value for
		// outputs that carry one (the DualSense report).
		if in.Buttons&protocol.BtnZL != 0 {
			in.LeftTrigger = 255
		}
		if in.Buttons&protocol.BtnZR != 0 {
			in.RightTrigger = 255
		}
		return in
	}
	f.Controller = remap(f.Controller)
	f.Player = remap(f.Player)
	byLabel := s.app.cfg.ABXYMode == "Switch" && kind != protocol.KindDualSense
	gameCube := !f.Pair && kind == protocol.KindGameCube
	f.Xbox = mapping.XboxReport(f.Player, byLabel, gameCube)
	return f
}

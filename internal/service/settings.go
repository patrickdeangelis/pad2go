package service

import (
	"maps"

	"github.com/patrickdeangelis/pad2go/internal/config"
)

// Settings is the part of the configuration the UI edits, in the UI's shape.
type Settings struct {
	Combine               bool              `json:"combine"`
	PairGyro              string            `json:"pairGyro"` // "Right" or "Left": a pair's motion source
	Gyro                  bool              `json:"gyro"`
	TouchpadPointer       bool              `json:"touchpadPointer"`
	Layout                string            `json:"layout"`      // "Xbox" (by position) or "Switch" (by label)
	Vibration             int               `json:"vibration"`   // 0-10, 5 = 100%
	Sensitivity           int               `json:"sensitivity"` // 1-5, yaw only
	Host                  string            `json:"host"`
	Port                  int               `json:"port"`
	Max                   int               `json:"max"`
	AutoDisconnectMinutes int               `json:"autoDisconnectMinutes"`
	Backend               string            `json:"backend"`
	HostMAC               string            `json:"hostMac"`
	Hold                  string            `json:"hold"`
	Trigger               string            `json:"trigger"`
	Haptics               bool              `json:"haptics"`
	Foreign               bool              `json:"foreign"`
	Deadzones             Deadzones         `json:"deadzones"`
	Remaps                Remaps            `json:"remaps"`
	DualSenseRemaps       map[string]string `json:"dualSenseRemaps"`
	DualSenseLighting     bool              `json:"dualSenseLighting"`
	DualSenseColor        string            `json:"dualSenseColor"`
	DualSenseBrightness   int               `json:"dualSenseBrightness"`
	DualSensePlayerLED    bool              `json:"dualSensePlayerLED"`
	DualSenseTriggerLeft  string            `json:"dualSenseTriggerLeft"`
	DualSenseTriggerRight string            `json:"dualSenseTriggerRight"`
	Holds                 map[string]string `json:"holds"` // Joy-Con address -> hold mode
	// OutputRemaps maps a scope ("global", "xbox", "dualsense" or "switch")
	// to source -> target protocol button names.
	OutputRemaps map[string]map[string]string `json:"outputRemaps"`
}

// Deadzones are radial stick deadzones in percent, per controller family.
type Deadzones struct {
	JoyCon    float64 `json:"joycon"`
	Pro       float64 `json:"pro"`
	GameCube  float64 `json:"gc"`
	DualSense float64 `json:"dualsense"`
}

// Remaps are the extra-button targets ("Default", "None" or a button name).
type Remaps struct {
	Home string `json:"home"`
	Capt string `json:"capt"`
	C    string `json:"c"`
	GL   string `json:"gl"`
	GR   string `json:"gr"`
	SLL  string `json:"sll"`
	SRL  string `json:"srl"`
	SLR  string `json:"slr"`
	SRR  string `json:"srr"`
}

// SettingsFrom extracts the UI settings from a configuration.
func SettingsFrom(c *config.Config) Settings {
	return Settings{
		Combine: c.CombineJoyCons, PairGyro: c.PairGyro, Gyro: c.CemuhookEnabled(), TouchpadPointer: c.TouchpadPointer, Layout: c.ABXYMode,
		Vibration: c.VibrationStrength, Sensitivity: c.CemuhookSensitivity,
		Host: c.CemuhookHost, Port: c.CemuhookPort, Max: c.MaxControllers,
		Backend: c.Output, HostMAC: c.HostMAC, Hold: c.DefaultHoldMode,
		AutoDisconnectMinutes: c.AutoDisconnectMinutes,
		Trigger:               c.GCTriggerMode, Haptics: c.ConnectHaptics, Foreign: c.AcceptForeignControllers,
		Deadzones: Deadzones{
			JoyCon:    c.Deadzone("joycon") * 100,
			Pro:       c.Deadzone("pro_controller") * 100,
			GameCube:  c.Deadzone("nso_gamecube_controller") * 100,
			DualSense: c.Deadzone("dualsense") * 100,
		},
		Remaps: Remaps{
			Home: c.HomeMapping, Capt: c.CaptMapping, C: c.CMapping, GL: c.GLMapping, GR: c.GRMapping,
			SLL: c.SLLMapping, SRL: c.SRLMapping, SLR: c.SLRMapping, SRR: c.SRRMapping,
		},
		Holds:             maps.Clone(c.JoyConHoldMode),
		OutputRemaps:      cloneOutputRemaps(c.OutputRemaps),
		DualSenseRemaps:   maps.Clone(c.DualSenseRemaps),
		DualSenseLighting: c.DualSenseLighting, DualSenseColor: c.DualSenseColor, DualSenseBrightness: c.DualSenseBrightness, DualSensePlayerLED: c.DualSensePlayerLED,
		DualSenseTriggerLeft: c.DualSenseTriggerLeft, DualSenseTriggerRight: c.DualSenseTriggerRight,
	}
}

// Apply writes s into c. Callers validate c afterwards.
func (s Settings) Apply(c *config.Config) {
	c.CombineJoyCons, c.PairGyro = s.Combine, s.PairGyro
	c.GyroPassthroughMode = "Default"
	if s.Gyro {
		c.GyroPassthroughMode = "Cemuhook"
	}
	c.TouchpadPointer = s.TouchpadPointer
	c.ABXYMode, c.VibrationStrength, c.CemuhookSensitivity = s.Layout, s.Vibration, s.Sensitivity
	c.CemuhookHost, c.CemuhookPort, c.MaxControllers = s.Host, s.Port, s.Max
	c.Output, c.HostMAC, c.DefaultHoldMode = s.Backend, s.HostMAC, s.Hold
	c.AutoDisconnectMinutes = s.AutoDisconnectMinutes
	c.GCTriggerMode, c.ConnectHaptics, c.AcceptForeignControllers = s.Trigger, s.Haptics, s.Foreign
	// Configs are copied by value during save; never mutate a map still shared
	// with the running configuration, especially when validation later fails.
	c.JoystickDeadzonePercent = maps.Clone(c.JoystickDeadzonePercent)
	if c.JoystickDeadzonePercent == nil {
		c.JoystickDeadzonePercent = map[string]float64{}
	}
	c.JoystickDeadzonePercent["joycon"] = s.Deadzones.JoyCon
	c.JoystickDeadzonePercent["pro_controller"] = s.Deadzones.Pro
	c.JoystickDeadzonePercent["nso_gamecube_controller"] = s.Deadzones.GameCube
	c.JoystickDeadzonePercent["dualsense"] = s.Deadzones.DualSense
	c.DualSenseRemaps = maps.Clone(s.DualSenseRemaps)
	c.DualSenseLighting, c.DualSenseColor, c.DualSenseBrightness, c.DualSensePlayerLED = s.DualSenseLighting, s.DualSenseColor, s.DualSenseBrightness, s.DualSensePlayerLED
	c.DualSenseTriggerLeft, c.DualSenseTriggerRight = s.DualSenseTriggerLeft, s.DualSenseTriggerRight
	r := s.Remaps
	c.HomeMapping, c.CaptMapping, c.CMapping, c.GLMapping, c.GRMapping = r.Home, r.Capt, r.C, r.GL, r.GR
	c.SLLMapping, c.SRLMapping, c.SLRMapping, c.SRRMapping = r.SLL, r.SRL, r.SLR, r.SRR
	c.JoyConHoldMode = maps.Clone(s.Holds)
	if c.JoyConHoldMode == nil {
		c.JoyConHoldMode = map[string]string{}
	}
	c.OutputRemaps = cleanOutputRemaps(s.OutputRemaps)
}

func cloneOutputRemaps(remaps map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(remaps))
	for scope, entries := range remaps {
		out[scope] = maps.Clone(entries)
	}
	return out
}

// cleanOutputRemaps drops no-op entries and empty scopes so restoring a table
// leaves the configuration file tidy. Unknown scopes are kept for Validate.
func cleanOutputRemaps(remaps map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(remaps))
	for scope, entries := range remaps {
		clean := make(map[string]string, len(entries))
		for source, target := range entries {
			if target != "" && target != "Default" {
				clean[source] = target
			}
		}
		if len(clean) > 0 {
			out[scope] = clean
		}
	}
	return out
}

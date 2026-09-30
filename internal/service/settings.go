package service

import (
	"maps"

	"github.com/angelispatrick/pad2go/internal/config"
)

// Settings is the part of the configuration the UI edits, in the UI's shape.
type Settings struct {
	Combine     bool              `json:"combine"`
	Gyro        bool              `json:"gyro"`
	Layout      string            `json:"layout"`      // "Xbox" (by position) or "Switch" (by label)
	Vibration   int               `json:"vibration"`   // 0-10, 5 = 100%
	Sensitivity int               `json:"sensitivity"` // 1-5, yaw only
	Host        string            `json:"host"`
	Port        int               `json:"port"`
	Max         int               `json:"max"`
	Backend     string            `json:"backend"`
	HostMAC     string            `json:"hostMac"`
	Hold        string            `json:"hold"`
	Trigger     string            `json:"trigger"`
	Haptics     bool              `json:"haptics"`
	Foreign     bool              `json:"foreign"`
	Deadzones   Deadzones         `json:"deadzones"`
	Remaps      Remaps            `json:"remaps"`
	Holds       map[string]string `json:"holds"` // Joy-Con address -> hold mode
}

// Deadzones are radial stick deadzones in percent, per controller family.
type Deadzones struct {
	JoyCon   float64 `json:"joycon"`
	Pro      float64 `json:"pro"`
	GameCube float64 `json:"gc"`
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
		Combine: c.CombineJoyCons, Gyro: c.CemuhookEnabled(), Layout: c.ABXYMode,
		Vibration: c.VibrationStrength, Sensitivity: c.CemuhookSensitivity,
		Host: c.CemuhookHost, Port: c.CemuhookPort, Max: c.MaxControllers,
		Backend: c.Output, HostMAC: c.HostMAC, Hold: c.DefaultHoldMode,
		Trigger: c.GCTriggerMode, Haptics: c.ConnectHaptics, Foreign: c.AcceptForeignControllers,
		Deadzones: Deadzones{
			JoyCon:   c.Deadzone("joycon") * 100,
			Pro:      c.Deadzone("pro_controller") * 100,
			GameCube: c.Deadzone("nso_gamecube_controller") * 100,
		},
		Remaps: Remaps{
			Home: c.HomeMapping, Capt: c.CaptMapping, C: c.CMapping, GL: c.GLMapping, GR: c.GRMapping,
			SLL: c.SLLMapping, SRL: c.SRLMapping, SLR: c.SLRMapping, SRR: c.SRRMapping,
		},
		Holds: maps.Clone(c.JoyConHoldMode),
	}
}

// Apply writes s into c. Callers validate c afterwards.
func (s Settings) Apply(c *config.Config) {
	c.CombineJoyCons = s.Combine
	c.GyroPassthroughMode = "Default"
	if s.Gyro {
		c.GyroPassthroughMode = "Cemuhook"
	}
	c.ABXYMode, c.VibrationStrength, c.CemuhookSensitivity = s.Layout, s.Vibration, s.Sensitivity
	c.CemuhookHost, c.CemuhookPort, c.MaxControllers = s.Host, s.Port, s.Max
	c.Output, c.HostMAC, c.DefaultHoldMode = s.Backend, s.HostMAC, s.Hold
	c.GCTriggerMode, c.ConnectHaptics, c.AcceptForeignControllers = s.Trigger, s.Haptics, s.Foreign
	if c.JoystickDeadzonePercent == nil {
		c.JoystickDeadzonePercent = map[string]float64{}
	}
	c.JoystickDeadzonePercent["joycon"] = s.Deadzones.JoyCon
	c.JoystickDeadzonePercent["pro_controller"] = s.Deadzones.Pro
	c.JoystickDeadzonePercent["nso_gamecube_controller"] = s.Deadzones.GameCube
	r := s.Remaps
	c.HomeMapping, c.CaptMapping, c.CMapping, c.GLMapping, c.GRMapping = r.Home, r.Capt, r.C, r.GL, r.GR
	c.SLLMapping, c.SRLMapping, c.SLRMapping, c.SRRMapping = r.SLL, r.SRL, r.SLR, r.SRR
	c.JoyConHoldMode = maps.Clone(s.Holds)
	if c.JoyConHoldMode == nil {
		c.JoyConHoldMode = map[string]string{}
	}
}

// Package config loads the YAML configuration. Key names follow the original
// Switch2Connect config.yaml where a setting exists in both, so an existing
// config file can be reused (unknown keys are ignored).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Hold modes for a single Joy-Con.
const (
	HoldVertical   = "Vertical"
	HoldHorizontal = "Horizontal"
)

// Config is the application configuration.
type Config struct {
	// Output selects the virtual gamepad backend: auto, vigem, uinput or none.
	Output string `yaml:"output"`
	// HostMAC is this computer's Bluetooth address, used to pair controllers so
	// they reconnect on a button press. Detected automatically where possible.
	HostMAC string `yaml:"host_mac"`
	// AcceptForeignControllers connects controllers bonded to another host.
	AcceptForeignControllers bool `yaml:"accept_foreign_controllers"`
	// MaxControllers caps the number of virtual pads (player slots).
	MaxControllers int `yaml:"max_controllers"`

	// ABXYMode is "Xbox" (positional, the default) or "Switch" (by label).
	ABXYMode string `yaml:"abxy_mode"`
	// CombineJoyCons merges a left and right Joy-Con into one pad.
	CombineJoyCons bool `yaml:"combine_joycons"`
	// JoyConHoldMode maps a Joy-Con address to Vertical or Horizontal.
	JoyConHoldMode map[string]string `yaml:"joycon_hold_mode"`
	// DefaultHoldMode is used for single Joy-Cons not listed in JoyConHoldMode.
	DefaultHoldMode string `yaml:"default_hold_mode"`

	// GCTriggerMode is "Hair Trigger", "100% at Bump" or "100% at Max".
	GCTriggerMode string `yaml:"gc_trigger_mode"`
	// GCTriggerCalibration maps an address to [Lmin, Lbump, Lmax, Rmin, Rbump, Rmax].
	GCTriggerCalibration map[string][]int `yaml:"gc_trigger_calibration_data"`

	// JoystickDeadzonePercent maps a family (joycon, pro_controller,
	// nso_gamecube_controller) to a radial deadzone in percent.
	JoystickDeadzonePercent map[string]float64 `yaml:"joystick_deadzone_percent"`

	// VibrationStrength scales rumble, 0-10 (5 = 100%).
	VibrationStrength int `yaml:"vibration_strength_xbox"`
	// RumbleDelayMs delays rumble to line up with audio.
	RumbleDelayMs int `yaml:"rumble_delay_ms"`
	// ConnectHaptics plays a short buzz when a controller connects.
	ConnectHaptics bool `yaml:"connect_haptics"`

	// GyroPassthroughMode "Cemuhook" enables the DSU motion server.
	GyroPassthroughMode string `yaml:"gyro_passthrough_mode"`
	CemuhookHost        string `yaml:"cemuhook_host"`
	CemuhookPort        int    `yaml:"cemuhook_port"`
	// CemuhookSensitivity (1-5) scales yaw only.
	CemuhookSensitivity int `yaml:"cemuhook_sensitivity"`

	// Back/extra button remaps: "Default", "None" or a Switch button name.
	HomeMapping string `yaml:"home_mapping"`
	CaptMapping string `yaml:"capt_mapping"`
	CMapping    string `yaml:"c_mapping"`
	GLMapping   string `yaml:"gl_mapping"`
	GRMapping   string `yaml:"gr_mapping"`
	SLLMapping  string `yaml:"sll_mapping"`
	SRLMapping  string `yaml:"srl_mapping"`
	SLRMapping  string `yaml:"slr_mapping"`
	SRRMapping  string `yaml:"srr_mapping"`
	// ButtonRemaps holds per-emulation-mode overrides; the "xbox" entry applies.
	ButtonRemaps map[string]Remaps `yaml:"button_remaps"`
}

// Remaps is a per-mode override block from button_remaps.
type Remaps struct {
	ABXYMode          string `yaml:"abxy_mode"`
	HomeMapping       string `yaml:"home_mapping"`
	CaptMapping       string `yaml:"capt_mapping"`
	CMapping          string `yaml:"c_mapping"`
	GLMapping         string `yaml:"gl_mapping"`
	GRMapping         string `yaml:"gr_mapping"`
	SLLMapping        string `yaml:"sll_mapping"`
	SRLMapping        string `yaml:"srl_mapping"`
	SLRMapping        string `yaml:"slr_mapping"`
	SRRMapping        string `yaml:"srr_mapping"`
	VibrationStrength *int   `yaml:"vibration_strength_xbox"`
}

// Default returns the default configuration.
func Default() *Config {
	return &Config{
		Output:               "auto",
		MaxControllers:       4,
		ABXYMode:             "Xbox",
		CombineJoyCons:       true,
		JoyConHoldMode:       map[string]string{},
		DefaultHoldMode:      HoldVertical,
		GCTriggerMode:        "100% at Bump",
		GCTriggerCalibration: map[string][]int{},
		JoystickDeadzonePercent: map[string]float64{
			"joycon": 3, "pro_controller": 3, "nso_gamecube_controller": 3,
		},
		VibrationStrength:   5,
		ConnectHaptics:      true,
		GyroPassthroughMode: "Default",
		CemuhookHost:        "127.0.0.1",
		CemuhookPort:        26760,
		CemuhookSensitivity: 1,
		HomeMapping:         "Default", CaptMapping: "Default", CMapping: "Default",
		GLMapping: "Default", GRMapping: "Default",
		SLLMapping: "Default", SRLMapping: "Default", SLRMapping: "Default", SRRMapping: "Default",
	}
}

// Load reads path, applying defaults for missing keys. A missing file yields
// the defaults.
func Load(path string) (*Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.applyModeRemaps()
	return cfg, cfg.Validate()
}

// applyModeRemaps lets button_remaps.xbox override the top-level mappings,
// matching the original's per-emulation-mode profiles.
func (c *Config) applyModeRemaps() {
	r, ok := c.ButtonRemaps["xbox"]
	if !ok {
		return
	}
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&c.ABXYMode, r.ABXYMode)
	set(&c.HomeMapping, r.HomeMapping)
	set(&c.CaptMapping, r.CaptMapping)
	set(&c.CMapping, r.CMapping)
	set(&c.GLMapping, r.GLMapping)
	set(&c.GRMapping, r.GRMapping)
	set(&c.SLLMapping, r.SLLMapping)
	set(&c.SRLMapping, r.SRLMapping)
	set(&c.SLRMapping, r.SLRMapping)
	set(&c.SRRMapping, r.SRRMapping)
	if r.VibrationStrength != nil {
		c.VibrationStrength = *r.VibrationStrength
	}
}

// Validate checks enumerated values.
func (c *Config) Validate() error {
	switch c.Output {
	case "auto", "vigem", "uinput", "none":
	default:
		return fmt.Errorf("output: unknown backend %q (want auto, vigem, uinput or none)", c.Output)
	}
	if c.ABXYMode != "Xbox" && c.ABXYMode != "Switch" {
		return fmt.Errorf("abxy_mode: want Xbox or Switch, got %q", c.ABXYMode)
	}
	if c.VibrationStrength < 0 || c.VibrationStrength > 10 {
		return fmt.Errorf("vibration_strength_xbox: want 0-10, got %d", c.VibrationStrength)
	}
	if c.MaxControllers < 1 || c.MaxControllers > 8 {
		return fmt.Errorf("max_controllers: want 1-8, got %d", c.MaxControllers)
	}
	if c.HostMAC != "" {
		if _, err := ParseMAC(c.HostMAC); err != nil {
			return fmt.Errorf("host_mac: %w", err)
		}
	}
	return nil
}

// CemuhookEnabled reports whether the DSU server should run.
func (c *Config) CemuhookEnabled() bool { return strings.EqualFold(c.GyroPassthroughMode, "Cemuhook") }

// HoldMode returns the configured hold mode for a Joy-Con address.
func (c *Config) HoldMode(addr string) string {
	if m, ok := c.JoyConHoldMode[addr]; ok {
		return m
	}
	if c.DefaultHoldMode != "" {
		return c.DefaultHoldMode
	}
	return HoldVertical
}

// Deadzone returns the radial deadzone (0-1) for a controller family.
func (c *Config) Deadzone(family string) float64 {
	if v, ok := c.JoystickDeadzonePercent[family]; ok {
		return v / 100
	}
	return 0.03
}

// ParseMAC parses "AA:BB:CC:DD:EE:FF" (or with dashes/no separators) into the
// big-endian integer form used by the pairing protocol.
func ParseMAC(s string) (uint64, error) {
	clean := strings.NewReplacer(":", "", "-", "", " ", "").Replace(s)
	if len(clean) != 12 {
		return 0, fmt.Errorf("invalid MAC %q", s)
	}
	var v uint64
	if _, err := fmt.Sscanf(clean, "%x", &v); err != nil {
		return 0, fmt.Errorf("invalid MAC %q", s)
	}
	return v, nil
}

// Sample is a commented starter config written by `switch2connect init`.
const Sample = `# Switch2Connect-Go configuration.
# Key names match the original Switch2Connect config.yaml where possible.

# Virtual gamepad backend: auto | vigem (Windows) | uinput (Linux) | none
output: auto

# This PC's Bluetooth MAC. Needed to pair controllers so they reconnect with a
# button press. Leave empty to auto-detect (Windows/Linux).
host_mac: ""
accept_foreign_controllers: false
max_controllers: 4

# Xbox = positional layout (Switch B -> Xbox A), Switch = match printed labels.
abxy_mode: Xbox
combine_joycons: true
default_hold_mode: Vertical     # Vertical | Horizontal (single Joy-Con)
joycon_hold_mode: {}            # per-address override, e.g. "AA:BB:...": Horizontal

gc_trigger_mode: 100% at Bump   # Hair Trigger | 100% at Bump | 100% at Max
joystick_deadzone_percent:
  joycon: 3
  pro_controller: 3
  nso_gamecube_controller: 3

vibration_strength_xbox: 5      # 0-10, 5 = 100%
rumble_delay_ms: 0
connect_haptics: true

# Set to Cemuhook to serve motion to Cemu / Ryujinx / Dolphin / yuzu forks.
gyro_passthrough_mode: Default
cemuhook_host: 127.0.0.1
cemuhook_port: 26760
cemuhook_sensitivity: 1

# Extra buttons: Default | None | any Switch button name (A, B, X, Y, L, R, ZL,
# ZR, MINUS, PLUS, L_STK, R_STK, UP, DOWN, LEFT, RIGHT, HOME, CAPT)
home_mapping: Default
capt_mapping: Default
c_mapping: Default
gl_mapping: Default
gr_mapping: Default
sll_mapping: Default
srl_mapping: Default
slr_mapping: Default
srr_mapping: Default
`

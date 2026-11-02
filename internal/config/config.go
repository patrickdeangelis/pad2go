// Package config loads the YAML configuration. Key names follow the original
// Switch2Connect config.yaml where a setting exists in both, so an existing
// config file can be reused (unknown keys are ignored).
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/patrickdeangelis/pad2go/internal/protocol"
	"gopkg.in/yaml.v3"
)

// Hold modes for a single Joy-Con.
const (
	HoldVertical   = "Vertical"
	HoldHorizontal = "Horizontal"
)

// Config is the application configuration.
type Config struct {
	// Output selects the virtual gamepad backend: auto, vigem, uinput,
	// dualsense, switch or none.
	Output string `yaml:"output"`
	// OutputOverrides maps a controller address (MAC) to a per-player output:
	// auto, xbox, dualsense, switch or none. The default applies to new players.
	OutputOverrides map[string]string `yaml:"output_overrides,omitempty"`
	// OutputRemaps holds virtual-button remaps. "global" applies to every
	// output; "xbox", "dualsense" and "switch" override it per output. Source
	// and target names are the protocol button names.
	OutputRemaps map[string]map[string]string `yaml:"output_remaps,omitempty"`
	// HostMAC is this computer's Bluetooth address, used to pair controllers so
	// they reconnect on a button press. Detected automatically where possible.
	HostMAC string `yaml:"host_mac"`
	// AcceptForeignControllers connects controllers bonded to another host.
	AcceptForeignControllers bool `yaml:"accept_foreign_controllers"`
	// MaxControllers caps the number of virtual pads (player slots).
	MaxControllers int `yaml:"max_controllers"`
	// AutoDisconnectMinutes disconnects controllers after input inactivity; zero disables it.
	AutoDisconnectMinutes int `yaml:"auto_disconnect_minutes"`

	// ABXYMode is "Xbox" (positional, the default) or "Switch" (by label).
	ABXYMode string `yaml:"abxy_mode"`
	// CombineJoyCons merges a left and right Joy-Con into one pad.
	CombineJoyCons bool `yaml:"combine_joycons"`
	// PairGyro picks which Joy-Con of a combined pair supplies the pair's
	// motion: "Right" (default) or "Left".
	PairGyro string `yaml:"joycon_pair_gyro"`
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
	// TouchpadPointer enables relative DualSense touchpad motion in DSU.
	TouchpadPointer bool   `yaml:"touchpad_pointer_enabled"`
	CemuhookHost    string `yaml:"cemuhook_host"`
	CemuhookPort    int    `yaml:"cemuhook_port"`
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
	// DualSenseRemaps maps physical DualSense button names to positional pad buttons.
	DualSenseRemaps       map[string]string `yaml:"dualsense_remaps,omitempty"`
	DualSenseLighting     bool              `yaml:"dualsense_lighting"`
	DualSenseColor        string            `yaml:"dualsense_color"`
	DualSenseBrightness   int               `yaml:"dualsense_brightness"`
	DualSensePlayerLED    bool              `yaml:"dualsense_player_led"`
	DualSenseTriggerLeft  string            `yaml:"dualsense_trigger_left"`
	DualSenseTriggerRight string            `yaml:"dualsense_trigger_right"`

	// UIPort is the local web interface port on 127.0.0.1 (0 disables it).
	UIPort int `yaml:"ui_port"`
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
		Output:          "auto",
		OutputOverrides: map[string]string{},
		OutputRemaps:    map[string]map[string]string{},
		DualSenseColor:  "#0080ff", DualSenseBrightness: 100, DualSensePlayerLED: true, DualSenseTriggerLeft: "off", DualSenseTriggerRight: "off",
		MaxControllers:       4,
		ABXYMode:             "Xbox",
		CombineJoyCons:       true,
		PairGyro:             "Right",
		JoyConHoldMode:       map[string]string{},
		DefaultHoldMode:      HoldVertical,
		GCTriggerMode:        "100% at Bump",
		GCTriggerCalibration: map[string][]int{},
		JoystickDeadzonePercent: map[string]float64{
			"joycon": 3, "pro_controller": 3, "nso_gamecube_controller": 3, "dualsense": 5,
		},
		VibrationStrength:   5,
		ConnectHaptics:      true,
		GyroPassthroughMode: "Default",
		CemuhookHost:        "127.0.0.1",
		CemuhookPort:        26760,
		CemuhookSensitivity: 1,
		UIPort:              7430,
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
	if c.AutoDisconnectMinutes < 0 || c.AutoDisconnectMinutes > 120 {
		return fmt.Errorf("auto_disconnect_minutes: want 0-120, got %d", c.AutoDisconnectMinutes)
	}
	for _, preset := range []string{c.DualSenseTriggerLeft, c.DualSenseTriggerRight} {
		if preset != "off" && preset != "resistance" && preset != "barrier" {
			return fmt.Errorf("dualsense trigger: unknown preset %q", preset)
		}
	}
	if len(c.DualSenseColor) != 7 || c.DualSenseColor[0] != '#' {
		return errors.New("dualsense_color: want #RRGGBB")
	}
	if _, err := hex.DecodeString(c.DualSenseColor[1:]); err != nil {
		return errors.New("dualsense_color: want #RRGGBB")
	}
	if c.DualSenseBrightness < 0 || c.DualSenseBrightness > 100 {
		return errors.New("dualsense_brightness: want 0-100")
	}
	switch c.Output {
	case "auto", "vigem", "uinput", "xbox", "dualsense", "switch", "none":
	default:
		return fmt.Errorf("output: unknown backend %q (want auto, vigem, uinput, xbox, dualsense, switch or none)", c.Output)
	}
	for addr, output := range c.OutputOverrides {
		switch output {
		case "auto", "xbox", "dualsense", "switch", "none":
		default:
			return fmt.Errorf("output_overrides[%s]: unknown output %q (want auto, xbox, dualsense, switch or none)", addr, output)
		}
	}
	for scope, remaps := range c.OutputRemaps {
		switch scope {
		case "global", "xbox", "dualsense", "switch":
		default:
			return fmt.Errorf("output_remaps: unknown scope %q (want global, xbox, dualsense or switch)", scope)
		}
		for source, target := range remaps {
			if _, ok := protocol.ButtonsByName[source]; !ok {
				return fmt.Errorf("output_remaps[%s]: unknown source button %q", scope, source)
			}
			switch target {
			case "", "Default", "None":
				continue
			}
			if _, ok := protocol.ButtonsByName[target]; !ok {
				return fmt.Errorf("output_remaps[%s][%s]: unknown target button %q", scope, source, target)
			}
		}
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
	if c.CemuhookPort < 1 || c.CemuhookPort > 65535 {
		return fmt.Errorf("cemuhook_port: want 1-65535, got %d", c.CemuhookPort)
	}
	if strings.TrimSpace(c.CemuhookHost) == "" {
		return errors.New("cemuhook_host: must not be empty")
	}
	if c.CemuhookSensitivity < 1 || c.CemuhookSensitivity > 5 {
		return fmt.Errorf("cemuhook_sensitivity: want 1-5, got %d", c.CemuhookSensitivity)
	}
	if c.UIPort < 0 || c.UIPort > 65535 {
		return fmt.Errorf("ui_port: want 0-65535, got %d", c.UIPort)
	}
	for family, v := range c.JoystickDeadzonePercent {
		if v < 0 || v > 50 {
			return fmt.Errorf("joystick_deadzone_percent.%s: want 0-50, got %v", family, v)
		}
	}
	for addr, mode := range c.JoyConHoldMode {
		if mode != HoldVertical && mode != HoldHorizontal {
			return fmt.Errorf("joycon_hold_mode.%s: want Vertical or Horizontal, got %q", addr, mode)
		}
	}
	if c.DefaultHoldMode != HoldVertical && c.DefaultHoldMode != HoldHorizontal {
		return fmt.Errorf("default_hold_mode: want Vertical or Horizontal, got %q", c.DefaultHoldMode)
	}
	if c.PairGyro != "Right" && c.PairGyro != "Left" {
		return fmt.Errorf("joycon_pair_gyro: want Right or Left, got %q", c.PairGyro)
	}
	switch c.GCTriggerMode {
	case "Hair Trigger", "100% at Bump", "100% at Max":
	default:
		return fmt.Errorf("gc_trigger_mode: unknown mode %q", c.GCTriggerMode)
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
	if family == "dualsense" {
		return 0.05
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

// Sample is a commented starter config written by `pad2go init`.
const Sample = `# pad2go configuration.
# Key names match the original Switch2Connect config.yaml where possible.

# Virtual gamepad backend: auto | vigem (Windows) | uinput (Linux) |
# dualsense (WinUHid on Windows, /dev/uhid on Linux) |
# switch (Switch Pro via WinUHid on Windows, /dev/uhid on Linux) | none
output: auto
# Per-player output overrides by controller address (MAC): auto | xbox | dualsense | switch | none
output_overrides: {}
# Virtual button remaps by output. "global" applies to every output and the
# per-output maps override it. Source/target are protocol button names; None
# clears a button.
# output_remaps:
#   global:
#     A: B
#   xbox:
#     CAPT: HOME

# This PC's Bluetooth MAC. Needed to pair controllers so they reconnect with a
# button press. Leave empty to auto-detect (Windows/Linux).
host_mac: ""
accept_foreign_controllers: false
max_controllers: 4

# Xbox = positional layout (Switch B -> Xbox A), Switch = match printed labels.
abxy_mode: Xbox
combine_joycons: true
joycon_pair_gyro: Right         # Right | Left: whose motion a combined pair sends
default_hold_mode: Vertical     # Vertical | Horizontal (single Joy-Con)
joycon_hold_mode: {}            # per-address override, e.g. "AA:BB:...": Horizontal

gc_trigger_mode: 100% at Bump   # Hair Trigger | 100% at Bump | 100% at Max
joystick_deadzone_percent:
  joycon: 3
  pro_controller: 3
  nso_gamecube_controller: 3
  dualsense: 5

# Physical DualSense options (Windows USB/Bluetooth HID).
dualsense_lighting: false       # true applies the color and player LED option
dualsense_color: "#0080ff"     # RGB color of the lightbar
dualsense_brightness: 100       # 0-100 percent
dualsense_player_led: true
dualsense_trigger_left: off      # off | resistance | barrier
dualsense_trigger_right: off
# dualsense_remaps:             # touch: B maps touchpad click to Xbox A
#   touch: B

vibration_strength_xbox: 5      # 0-10, 5 = 100%
rumble_delay_ms: 0
connect_haptics: true

# Set to Cemuhook to serve motion to Cemu / Ryujinx / Dolphin / yuzu forks.
gyro_passthrough_mode: Default
touchpad_pointer_enabled: false
cemuhook_host: 127.0.0.1
cemuhook_port: 26760
cemuhook_sensitivity: 1

# Local web interface at http://127.0.0.1:<ui_port> (0 disables it).
ui_port: 7430

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

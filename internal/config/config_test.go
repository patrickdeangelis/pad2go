package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMissingFileGivesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Output != "auto" || !cfg.CombineJoyCons || cfg.VibrationStrength != 5 {
		t.Fatalf("%+v", cfg)
	}
}

func TestSampleParses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte(Sample), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("sample should equal defaults:\n%+v\n%+v", cfg, Default())
	}
}

// The original Switch2Connect config.yaml loads, and its xbox profile applies.
func TestOriginalConfigCompatible(t *testing.T) {
	cfg, err := Load("testdata/original_config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ABXYMode != "Xbox" {
		t.Fatalf("abxy_mode %q", cfg.ABXYMode)
	}
	if cfg.CMapping != "Calibration" || cfg.VibrationStrength != 5 {
		t.Fatalf("xbox remaps not applied: c=%q vib=%d", cfg.CMapping, cfg.VibrationStrength)
	}
	if cfg.GCTriggerMode != "100% at Max" {
		t.Fatalf("gc_trigger_mode %q", cfg.GCTriggerMode)
	}
}

func TestValidate(t *testing.T) {
	bad := []func(*Config){
		func(c *Config) { c.Output = "dinput" },
		func(c *Config) { c.ABXYMode = "PS" },
		func(c *Config) { c.VibrationStrength = 11 },
		func(c *Config) { c.MaxControllers = 0 },
		func(c *Config) { c.HostMAC = "zz" },
	}
	for i, mut := range bad {
		c := Default()
		mut(c)
		if c.Validate() == nil {
			t.Errorf("case %d should fail validation", i)
		}
	}
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestParseMAC(t *testing.T) {
	for _, s := range []string{"AA:BB:CC:DD:EE:FF", "aa-bb-cc-dd-ee-ff", "AABBCCDDEEFF"} {
		v, err := ParseMAC(s)
		if err != nil || v != 0xAABBCCDDEEFF {
			t.Errorf("%s: %x %v", s, v, err)
		}
	}
	if _, err := ParseMAC("AA:BB"); err == nil {
		t.Fatal("short MAC should fail")
	}
}

func TestHoldModeAndDeadzone(t *testing.T) {
	c := Default()
	c.JoyConHoldMode["X"] = HoldHorizontal
	if c.HoldMode("X") != HoldHorizontal || c.HoldMode("Y") != HoldVertical {
		t.Fatal("hold mode")
	}
	if c.Deadzone("joycon") != 0.03 || c.Deadzone("unknown") != 0.03 {
		t.Fatal("deadzone")
	}
	if c.CemuhookEnabled() {
		t.Fatal("cemuhook should default off")
	}
}

func TestSaveRoundTripKeepsCommentsAndUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	orig := "# my notes\nunknown_key: keep me\nabxy_mode: Xbox # layout\nbutton_remaps:\n  xbox:\n    abxy_mode: Xbox\n    rumble_mode: Switch\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ABXYMode = "Switch"
	cfg.JoyConHoldMode["AA:BB:CC:DD:EE:FF"] = HoldHorizontal
	cfg.JoystickDeadzonePercent["joycon"] = 7
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{"# my notes", "unknown_key: keep me", "# layout", "rumble_mode: Switch"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("saved file lost %q:\n%s", want, data)
		}
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// button_remaps.xbox.abxy_mode was updated too, so it doesn't override.
	if again.ABXYMode != "Switch" || again.HoldMode("AA:BB:CC:DD:EE:FF") != HoldHorizontal || again.Deadzone("joycon") != 0.07 {
		t.Fatalf("reloaded %+v", again)
	}
}

func TestSaveNewFileStartsFromSample(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.yaml")
	cfg := Default()
	cfg.VibrationStrength = 8
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# Virtual gamepad backend") {
		t.Fatal("sample comments missing")
	}
	again, _ := Load(path)
	if again.VibrationStrength != 8 {
		t.Fatalf("vibration %d", again.VibrationStrength)
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	cfg := Default()
	cfg.CemuhookPort = 0
	if err := cfg.Save(filepath.Join(t.TempDir(), "x.yaml")); err == nil {
		t.Fatal("invalid config must not be saved")
	}
}

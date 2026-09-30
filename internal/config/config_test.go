package config

import (
	"os"
	"path/filepath"
	"reflect"
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

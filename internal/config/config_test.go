package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"adassay.com/internal/core"
)

func TestEmbeddedDefaultIsValid(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("embedded default: %v", err)
	}
	if len(cfg.L2.Weights) != len(Features) {
		t.Errorf("weights = %v, want one per feature %v", cfg.L2.Weights, Features)
	}
	if cfg.Judge.Timeout == 0 {
		t.Error("judge.timeout not decoded")
	}
	for _, s := range cfg.L2.Shortcuts {
		if s.ParsedVerdict() != core.Drop {
			t.Errorf("shortcut %v verdict = %v, want drop", s.Features, s.ParsedVerdict())
		}
	}
}

func TestLoadOverridesDefaults(t *testing.T) {
	path := write(t, "l2:\n  hi: 0.9\n  weights:\n    promo_code: 4.0\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.L2.Hi != 0.9 {
		t.Errorf("hi = %v, want 0.9", cfg.L2.Hi)
	}
	if cfg.L2.Weights["promo_code"] != 4.0 {
		t.Errorf("promo_code weight = %v, want 4.0", cfg.L2.Weights["promo_code"])
	}
	if _, ok := cfg.L2.Weights["rel_sponsored"]; !ok {
		t.Error("override dropped untouched weights instead of merging")
	}
	if cfg.Judge.Model == "" {
		t.Error("override dropped the judge section")
	}
}

func TestLoadRejectsUnknownKeyAndMissingFile(t *testing.T) {
	if _, err := Load(write(t, "l2:\n  hii: 0.9\n")); err == nil {
		t.Error("unknown key accepted")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Error("missing file accepted")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"lo above hi", func(c *Config) { c.L2.Lo = 0.9; c.L2.Hi = 0.5 }, "must be below"},
		{"lo equals hi", func(c *Config) { c.L2.Lo = c.L2.Hi }, "must be below"},
		{"hi out of range", func(c *Config) { c.L2.Hi = 1.5 }, "within 0..1"},
		{"lo out of range", func(c *Config) { c.L2.Lo = -0.1 }, "within 0..1"},
		{"missing weight", func(c *Config) { delete(c.L2.Weights, "disclaimer") }, `no weight for feature "disclaimer"`},
		{"negative weight", func(c *Config) { c.L2.Weights["promo_code"] = -1 }, "negative"},
		{"unknown weight", func(c *Config) { c.L2.Weights["vibes"] = 1 }, "unknown feature"},
		{"empty shortcut", func(c *Config) { c.L2.Shortcuts = []Shortcut{{Verdict: "drop"}} }, "no features"},
		{"unknown shortcut feature", func(c *Config) {
			c.L2.Shortcuts = []Shortcut{{Features: []string{"vibes"}, Verdict: "drop"}}
		}, "unknown feature"},
		{"bad shortcut verdict", func(c *Config) {
			c.L2.Shortcuts = []Shortcut{{Features: []string{"promo_code"}, Verdict: "nuke"}}
		}, "unknown verdict"},
		{"empty patterns", func(c *Config) { c.L2.Patterns.Disclaimers = nil }, "disclaimers is empty"},

		{"blank pattern", func(c *Config) {
			c.L2.Patterns.Disclaimers = append(c.L2.Patterns.Disclaimers, " ")
		}, "disclaimers["},
		{"blank imperative", func(c *Config) {
			c.L1.Imperatives = append(c.L1.Imperatives, "")
		}, "imperatives["},
		{"zero min length", func(c *Config) { c.L1.MinLength = 0 }, "min_length"},
		{"empty imperatives", func(c *Config) { c.L1.Imperatives = nil }, "imperatives is empty"},
		{"empty agent names", func(c *Config) { c.L1.AgentNames = nil }, "agent_names is empty"},
		{"zero min visits", func(c *Config) { c.L3.MinVisits = 0 }, "min_visits"},
		{"zero half life", func(c *Config) { c.L3.HalfLifeDays = 0 }, "half_life_days"},
		{"negative penalty", func(c *Config) { c.L3.FindingPenalty = -1 }, "finding_penalty"},
		{"shift out of range", func(c *Config) { c.L3.MaxShift = 2 }, "max_shift"},
		{"no judge endpoint", func(c *Config) { c.Judge.Endpoint = "" }, "judge.endpoint"},
		{"no judge model", func(c *Config) { c.Judge.Model = "" }, "judge.model"},
		{"no judge timeout", func(c *Config) { c.Judge.Timeout = 0 }, "judge.timeout"},
		{"no judge batch", func(c *Config) { c.Judge.BatchSize = 0 }, "judge.batch_size"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load("")
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			tt.mut(cfg)
			err = cfg.Validate()
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEmptyOverrideFileKeepsDefaults(t *testing.T) {
	for name, body := range map[string]string{"empty": "", "only comments": "# nothing to change\n"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rules.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			def, err := Load("")
			if err != nil {
				t.Fatalf("load defaults: %v", err)
			}
			if cfg.L2.Hi != def.L2.Hi || cfg.L2.Lo != def.L2.Lo {
				t.Errorf("thresholds = %v/%v, want the defaults %v/%v", cfg.L2.Hi, cfg.L2.Lo, def.L2.Hi, def.L2.Lo)
			}
		})
	}
}

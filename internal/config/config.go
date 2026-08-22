// Package config holds the tunable part of the filter: L1 significance
// thresholds, L2 feature weights and patterns, the L3 domain modifier and the
// judge endpoint. Everything here is expected to be re-tuned often, so it lives
// in rules.yaml instead of the binary.
package config

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/belaytzev/adfilter/internal/core"
)

//go:embed rules.yaml
var defaultRules []byte

const (
	FeatureRelSponsored = "rel_sponsored"
	FeaturePromoCode    = "promo_code"
	FeatureAffiliate    = "affiliate_link"
	FeatureDisclaimer   = "disclaimer"
)

// Features is the canonical list of L2 deterministic features. A weight must
// exist for every one of them: a missing weight would silently score as zero.
var Features = []string{FeatureRelSponsored, FeaturePromoCode, FeatureAffiliate, FeatureDisclaimer}

type Config struct {
	L1    L1    `yaml:"l1"`
	L2    L2    `yaml:"l2"`
	L3    L3    `yaml:"l3"`
	Judge Judge `yaml:"judge"`
}

type L1 struct {
	MinLength      int      `yaml:"min_length"`
	LongAttrLength int      `yaml:"long_attr_length"`
	Imperatives    []string `yaml:"imperatives"`
	AgentNames     []string `yaml:"agent_names"`
}

type L2 struct {
	Hi        float64            `yaml:"hi"`
	Lo        float64            `yaml:"lo"`
	Bias      float64            `yaml:"bias"`
	Weights   map[string]float64 `yaml:"weights"`
	Shortcuts []Shortcut         `yaml:"shortcuts"`
	Patterns  Patterns           `yaml:"patterns"`
}

// Shortcut bypasses the weighted sum: if every feature fired, the verdict is
// taken as is.
type Shortcut struct {
	Features []string `yaml:"features"`
	Verdict  string   `yaml:"verdict"`
}

func (s Shortcut) ParsedVerdict() core.Verdict {
	v, _ := core.ParseVerdict(s.Verdict)
	return v
}

type Patterns struct {
	PromoWords      []string `yaml:"promo_words"`
	Disclaimers     []string `yaml:"disclaimers"`
	AffiliateParams []string `yaml:"affiliate_params"`
	AffiliateHosts  []string `yaml:"affiliate_hosts"`
}

type L3 struct {
	MinVisits      int     `yaml:"min_visits"`
	HalfLifeDays   float64 `yaml:"half_life_days"`
	FindingPenalty float64 `yaml:"finding_penalty"`
	// MaxShift is the additive shift applied to a segment score at full domain
	// distrust; the result is clamped to 0..1.
	MaxShift float64 `yaml:"max_shift"`
}

type Judge struct {
	Endpoint  string        `yaml:"endpoint"`
	Model     string        `yaml:"model"`
	Timeout   time.Duration `yaml:"timeout"`
	BatchSize int           `yaml:"batch_size"`
}

// Load returns the embedded defaults with the file at path decoded on top of
// them, so an external file only has to carry the keys it changes. An empty
// path returns the defaults alone.
func Load(path string) (*Config, error) {
	cfg := &Config{}
	if err := decode(defaultRules, cfg); err != nil {
		return nil, fmt.Errorf("config: embedded defaults: %w", err)
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		if err := decode(data, cfg); err != nil {
			return nil, fmt.Errorf("config: %s: %w", path, err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func decode(data []byte, cfg *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(cfg)
}

func (c *Config) Validate() error {
	if c.L1.MinLength <= 0 {
		return fmt.Errorf("config: l1.min_length must be positive")
	}
	if c.L1.LongAttrLength <= 0 {
		return fmt.Errorf("config: l1.long_attr_length must be positive")
	}
	if len(c.L1.Imperatives) == 0 {
		return fmt.Errorf("config: l1.imperatives is empty")
	}
	if len(c.L1.AgentNames) == 0 {
		return fmt.Errorf("config: l1.agent_names is empty")
	}

	if c.L2.Lo < 0 || c.L2.Hi > 1 {
		return fmt.Errorf("config: l2.lo and l2.hi must be within 0..1")
	}
	if c.L2.Lo >= c.L2.Hi {
		return fmt.Errorf("config: l2.lo (%v) must be below l2.hi (%v)", c.L2.Lo, c.L2.Hi)
	}
	for _, f := range Features {
		w, ok := c.L2.Weights[f]
		if !ok {
			return fmt.Errorf("config: no weight for feature %q", f)
		}
		if w < 0 {
			return fmt.Errorf("config: weight for feature %q is negative", f)
		}
	}
	for f := range c.L2.Weights {
		if !known(f) {
			return fmt.Errorf("config: weight for unknown feature %q", f)
		}
	}
	for i, s := range c.L2.Shortcuts {
		if len(s.Features) == 0 {
			return fmt.Errorf("config: shortcut %d has no features", i)
		}
		for _, f := range s.Features {
			if !known(f) {
				return fmt.Errorf("config: shortcut %d references unknown feature %q", i, f)
			}
		}
		if _, err := core.ParseVerdict(s.Verdict); err != nil {
			return fmt.Errorf("config: shortcut %d: %w", i, err)
		}
	}
	for name, list := range map[string][]string{
		"promo_words":      c.L2.Patterns.PromoWords,
		"disclaimers":      c.L2.Patterns.Disclaimers,
		"affiliate_params": c.L2.Patterns.AffiliateParams,
		"affiliate_hosts":  c.L2.Patterns.AffiliateHosts,
	} {
		if len(list) == 0 {
			return fmt.Errorf("config: l2.patterns.%s is empty", name)
		}
	}

	if c.L3.MinVisits <= 0 {
		return fmt.Errorf("config: l3.min_visits must be positive")
	}
	if c.L3.HalfLifeDays <= 0 {
		return fmt.Errorf("config: l3.half_life_days must be positive")
	}
	if c.L3.FindingPenalty < 0 {
		return fmt.Errorf("config: l3.finding_penalty must not be negative")
	}
	if c.L3.MaxShift < 0 || c.L3.MaxShift > 1 {
		return fmt.Errorf("config: l3.max_shift must be within 0..1")
	}

	if c.Judge.Endpoint == "" {
		return fmt.Errorf("config: judge.endpoint is empty")
	}
	if c.Judge.Model == "" {
		return fmt.Errorf("config: judge.model is empty")
	}
	if c.Judge.Timeout <= 0 {
		return fmt.Errorf("config: judge.timeout must be positive")
	}
	if c.Judge.BatchSize <= 0 {
		return fmt.Errorf("config: judge.batch_size must be positive")
	}
	return nil
}

func known(feature string) bool {
	for _, f := range Features {
		if f == feature {
			return true
		}
	}
	return false
}

package config

import (
	"fmt"
	"regexp"
)

// AIPolicyConfig contains portable preferences, never credentials or host paths.
type AIPolicyConfig struct {
	Enabled     bool             `yaml:"enabled" json:"enabled"`
	Revision    string           `yaml:"revision" json:"revision"`
	Targets     []AIPolicyTarget `yaml:"targets" json:"targets"`
	MaxSwitches int              `yaml:"max_switches,omitempty" json:"max_switches"`
}
type AIPolicyTarget struct {
	ID           string   `yaml:"id" json:"id"`
	Agent        string   `yaml:"agent" json:"agent"`
	Model        string   `yaml:"model" json:"model"`
	Effort       string   `yaml:"effort,omitempty" json:"effort,omitempty"`
	Billing      string   `yaml:"billing" json:"billing"`
	Workloads    []string `yaml:"workloads" json:"workloads"`
	Capabilities []string `yaml:"capabilities,omitempty" json:"capabilities"`
	Validated    bool     `yaml:"validated" json:"validated"`
	Version      string   `yaml:"version" json:"version"`
	Priority     int      `yaml:"priority,omitempty" json:"priority"`
}

func (c *AIPolicyConfig) Clone() *AIPolicyConfig {
	if c == nil {
		return nil
	}
	out := *c
	out.Targets = append([]AIPolicyTarget{}, c.Targets...)
	for i := range out.Targets {
		out.Targets[i].Workloads = append([]string{}, c.Targets[i].Workloads...)
		out.Targets[i].Capabilities = append([]string{}, c.Targets[i].Capabilities...)
	}
	return &out
}
func (c *AIPolicyConfig) SwitchLimit() int {
	if c == nil || c.MaxSwitches == 0 {
		return 2
	}
	return c.MaxSwitches
}

var policyID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
var policyToken = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]*$`)

func ValidateAIPolicy(c *AIPolicyConfig) error {
	if c == nil {
		return nil
	}
	if c.MaxSwitches < 0 || c.MaxSwitches > 2 {
		return fmt.Errorf("ai.policy.max_switches must be between 0 (default 2) and 2")
	}
	if c.Enabled && c.Revision == "" {
		return fmt.Errorf("enabled ai.policy requires revision")
	}
	seen := map[string]bool{}
	for _, t := range c.Targets {
		if !policyID.MatchString(t.ID) || seen[t.ID] {
			return fmt.Errorf("ai.policy target ID must be unique and portable: %q", t.ID)
		}
		seen[t.ID] = true
		switch t.Agent {
		case "claude", "codex", "grok", "kimi", "opencode", "qwen", "kiro", "pi":
		default:
			return fmt.Errorf("unknown ai.policy agent %q", t.Agent)
		}
		if t.Billing != "" && t.Billing != "subscription" && t.Billing != "dgx" {
			return fmt.Errorf("target %s billing must be subscription or dgx", t.ID)
		}
		if t.Validated && (t.Model == "" || t.Version == "" || t.Billing == "") {
			return fmt.Errorf("validated target %s requires model, version and billing", t.ID)
		}
		if t.Model != "" && !policyToken.MatchString(t.Model) {
			return fmt.Errorf("invalid target %s model", t.ID)
		}
		switch t.Effort {
		case "", "balanced", "high", "low", "medium":
		default:
			return fmt.Errorf("invalid target %s effort", t.ID)
		}
		for _, w := range t.Workloads {
			switch w {
			case "routine", "implementation", "deep-analysis", "independent-review", "documents-teaching", "visual-production":
			default:
				return fmt.Errorf("invalid target %s workload %q", t.ID, w)
			}
		}
		for _, cap := range t.Capabilities {
			if !policyToken.MatchString(cap) {
				return fmt.Errorf("invalid target %s capability", t.ID)
			}
		}
	}
	return nil
}

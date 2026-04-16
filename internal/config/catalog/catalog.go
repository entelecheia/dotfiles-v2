// Package catalog exposes the embedded macOS application catalog used by the
// `apps` subcommands and the init wizard.
package catalog

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed macos-apps.yaml
var macOSAppsYAML []byte

// MacApp describes a single cask entry in the catalog.
type MacApp struct {
	Token string `yaml:"token"`
	Name  string `yaml:"name"`
}

// MacAppGroup groups related casks under a human-readable label.
type MacAppGroup struct {
	Name string   `yaml:"name"`
	Apps []MacApp `yaml:"apps"`
}

// MacApps is the root of the catalog.
type MacApps struct {
	Defaults []string      `yaml:"defaults"`
	Groups   []MacAppGroup `yaml:"groups"`
}

// LoadMacApps parses the embedded macOS app catalog.
func LoadMacApps() (*MacApps, error) {
	var c MacApps
	if err := yaml.Unmarshal(macOSAppsYAML, &c); err != nil {
		return nil, fmt.Errorf("parse macos-apps.yaml: %w", err)
	}
	return &c, nil
}

// AllTokens returns every token across all groups in definition order.
// Duplicates are filtered (first occurrence wins).
func (c *MacApps) AllTokens() []string {
	seen := make(map[string]bool)
	var out []string
	for _, g := range c.Groups {
		for _, a := range g.Apps {
			if seen[a.Token] {
				continue
			}
			seen[a.Token] = true
			out = append(out, a.Token)
		}
	}
	return out
}

// DisplayName looks up a human-readable name for a token. Falls back to the
// token itself if not found.
func (c *MacApps) DisplayName(token string) string {
	for _, g := range c.Groups {
		for _, a := range g.Apps {
			if a.Token == token {
				return a.Name
			}
		}
	}
	return token
}

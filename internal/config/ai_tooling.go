package config

import (
	"fmt"
	"strings"
)

// ValidateAITooling validates portable values without coupling config to installers.
func ValidateAITooling(c *AIToolingConfig) error {
	if c == nil {
		return nil
	}
	for field, values := range map[string][]string{"agents": c.Agents, "tools": c.Tools, "skills": c.Skills} {
		seen := map[string]bool{}
		for _, value := range values {
			if strings.TrimSpace(value) != value || value == "" || strings.ContainsAny(value, "/\\\n\r\t") || value == "." || value == ".." {
				return fmt.Errorf("modules.ai.tooling.%s contains invalid identifier %q", field, value)
			}
			if seen[value] {
				return fmt.Errorf("modules.ai.tooling.%s contains duplicate %q", field, value)
			}
			seen[value] = true
		}
	}
	return nil
}

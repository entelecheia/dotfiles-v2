package catalog

import "testing"

func TestLoadMacApps(t *testing.T) {
	c, err := LoadMacApps()
	if err != nil {
		t.Fatalf("LoadMacApps: %v", err)
	}
	if len(c.Defaults) == 0 {
		t.Fatal("defaults empty")
	}
	if len(c.Groups) == 0 {
		t.Fatal("groups empty")
	}
	// Every default must appear somewhere in a group
	present := make(map[string]bool)
	for _, g := range c.Groups {
		for _, a := range g.Apps {
			if a.Token == "" {
				t.Errorf("group %q has entry with empty token", g.Name)
			}
			if a.Name == "" {
				t.Errorf("%q: empty display name", a.Token)
			}
			present[a.Token] = true
		}
	}
	for _, d := range c.Defaults {
		if !present[d] {
			t.Errorf("default %q not defined in any group", d)
		}
	}
	// AllTokens must be unique
	tokens := c.AllTokens()
	seen := make(map[string]bool)
	for _, t2 := range tokens {
		if seen[t2] {
			t.Errorf("AllTokens duplicate: %q", t2)
		}
		seen[t2] = true
	}
	// DisplayName fallback
	if got := c.DisplayName("__not-a-real-token__"); got != "__not-a-real-token__" {
		t.Errorf("DisplayName fallback: got %q", got)
	}
}

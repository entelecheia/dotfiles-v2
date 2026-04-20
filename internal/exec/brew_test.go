package exec

import (
	"encoding/json"
	"testing"
)

// TestExtractAppName covers every shape that brew cask JSON emits for an
// `app` artifact entry: a plain string, a tuple with a target override, and
// a tuple with only a source. Keeping this close to the production function
// makes regressions cheap to catch when brew's schema shifts.
func TestExtractAppName(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain string", `"Raycast.app"`, "Raycast.app"},
		{"path-qualified string", `"Some/Nested/Path/Foo.app"`, "Foo.app"},
		{"tuple with target", `["Source.app", {"target": "/Applications/Target.app"}]`, "Target.app"},
		{"tuple source only", `["Source.app"]`, "Source.app"},
		{"empty tuple", `[]`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractAppName(json.RawMessage(tc.raw))
			if got != tc.want {
				t.Errorf("extractAppName(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

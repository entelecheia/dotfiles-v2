package cli

import (
	"testing"
)

func TestPlistInteger(t *testing.T) {
	body := `<dict><key>StartInterval</key><integer>900</integer></dict>`
	if got := plistInteger(body, "StartInterval"); got != 900 {
		t.Fatalf("plistInteger = %d, want 900", got)
	}
	if got := plistInteger(body, "Missing"); got != 0 {
		t.Fatalf("missing plist integer = %d, want 0", got)
	}
}

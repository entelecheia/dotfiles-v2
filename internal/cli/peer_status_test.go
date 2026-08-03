package cli

import (
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/syncer"
)

func TestPeerRuntimePolicyAlwaysDisablesDeletes(t *testing.T) {
	cfg := &syncer.Config{
		Propagation: syncer.PropagationPolicy{Delete: true},
	}
	enforcePeerRuntimePolicy(cfg)
	if cfg.Propagation.Delete {
		t.Fatal("peer runtime policy must never propagate deletes")
	}
	if !cfg.Propagation.Create || !cfg.Propagation.Update {
		t.Fatal("peer runtime policy must retain safe bidirectional create/update")
	}
}

func TestPlistInteger(t *testing.T) {
	body := `<dict><key>StartInterval</key><integer>900</integer></dict>`
	if got := plistInteger(body, "StartInterval"); got != 900 {
		t.Fatalf("plistInteger = %d, want 900", got)
	}
	if got := plistInteger(body, "Missing"); got != 0 {
		t.Fatalf("missing plist integer = %d, want 0", got)
	}
}

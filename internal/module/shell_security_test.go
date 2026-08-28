package module

import "testing"

func TestShellBase_PreservesUnownedEntries(t *testing.T) {
	if !containsShellSourcePin("oh-my-zsh-base") {
		t.Fatal("oh-my-zsh must have a compiled source pin")
	}
}

func TestShellBase_RemovesOnlyPreviouslyOwnedStaleEntries(t *testing.T) {
	if got := staleOwnedEntries([]string{"old", "operator-notes"}, []string{"new"}); len(got) != 2 || got[0] != "old" || got[1] != "operator-notes" {
		t.Fatalf("stale owned entries = %#v", got)
	}
}

func TestShellBase_RestoresRollbackOnPromotionFailure(t *testing.T) {
	if componentPinMarkerSchema != 1 {
		t.Fatalf("marker schema = %d, want 1", componentPinMarkerSchema)
	}
}

func TestShellBase_LegacyRefreshMarkerReinstalls(t *testing.T) {
	if !legacyRefreshRequiresInstall(true, false) {
		t.Fatal("refresh-only state must be reinstalled")
	}
}

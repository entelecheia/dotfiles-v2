package gsync

import "testing"

func TestCollectPlanInventory_UsesRequestedFingerprintMode(t *testing.T) {
	f := newIntakeFixture(t)
	f.writeLocal("assets/report.pdf", "payload")

	filter, err := newSyncFilter(f.cfg, f.mirror)
	if err != nil {
		t.Fatalf("newSyncFilter: %v", err)
	}

	fast, err := collectPlanInventory(f.local, filter, map[string]bool{}, FingerprintFast)
	if err != nil {
		t.Fatalf("collectPlanInventory fast: %v", err)
	}
	if got := fast.files["assets/report.pdf"].Sha; got != "" {
		t.Fatalf("fast inventory unexpectedly hashed file: %q", got)
	}

	strict, err := collectPlanInventory(f.local, filter, map[string]bool{}, FingerprintStrict)
	if err != nil {
		t.Fatalf("collectPlanInventory strict: %v", err)
	}
	if got := strict.files["assets/report.pdf"].Sha; got == "" {
		t.Fatal("strict inventory did not hash file")
	}
}

package config

import "testing"

func testPolicy() *AIPolicyConfig {
	return &AIPolicyConfig{Enabled: true, Revision: "1", Targets: []AIPolicyTarget{{ID: "primary", Agent: "claude", Model: "sonnet", Version: "2.1.283", Billing: "subscription", Validated: true, Workloads: []string{"routine"}, Capabilities: []string{"code"}}}}
}
func TestPolicyPersistenceAndClone(t *testing.T) {
	home := t.TempDir()
	state := &UserState{Name: "Test", Profile: "full"}
	state.Modules.AI.Policy = testPolicy()
	if err := SaveStateForHome(home, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadStateFrom(StatePathForHome(home))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SchemaVersion != currentSchemaVersion {
		t.Fatalf("schema %d", loaded.SchemaVersion)
	}
	cfg := &Config{}
	ApplyStateToConfig(cfg, loaded)
	cfg.Modules.AI.Policy.Targets[0].Workloads[0] = "implementation"
	cfg.Modules.AI.Policy.Targets[0].Capabilities[0] = "text"
	if loaded.Modules.AI.Policy.Targets[0].Workloads[0] != "routine" || loaded.Modules.AI.Policy.Targets[0].Capabilities[0] != "code" {
		t.Fatal("clone aliases persisted policy")
	}
	if cfg.Modules.AI.Policy.SwitchLimit() != 2 {
		t.Fatal("incorrect default switch limit")
	}
}
func TestPolicyValidation(t *testing.T) {
	for _, mutate := range []func(*AIPolicyConfig){
		func(p *AIPolicyConfig) { p.MaxSwitches = 3 }, func(p *AIPolicyConfig) { p.Revision = "" }, func(p *AIPolicyConfig) { p.Targets[0].Billing = "payg" }, func(p *AIPolicyConfig) { p.Targets[0].Model = "" }, func(p *AIPolicyConfig) { p.Targets[0].Agent = "unknown" }, func(p *AIPolicyConfig) { p.Targets[0].Workloads = []string{"unknown"} }, func(p *AIPolicyConfig) { p.Targets = append(p.Targets, p.Targets[0]) },
	} {
		p := testPolicy()
		mutate(p)
		if err := ValidateAIPolicy(p); err == nil {
			t.Fatal("accepted invalid policy")
		}
	}
	if err := ValidateAIPolicy(testPolicy()); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyRejectsUnsafeIDs(t *testing.T) {
	for _, id := range []string{"../escape", "a/b", "x:profile", "", "a b"} {
		p := testPolicy()
		p.Targets[0].ID = id
		if err := ValidateAIPolicy(p); err == nil {
			t.Fatalf("unsafe ID accepted: %q", id)
		}
	}
}

func TestPolicyMergeClonesAndAllowsDisable(t *testing.T) {
	base := ModulesConfig{AI: AIConfig{Policy: testPolicy()}}
	merged := mergeModules(base, ModulesConfig{})
	merged.AI.Policy.Targets[0].Workloads[0] = "implementation"
	if base.AI.Policy.Targets[0].Workloads[0] != "routine" {
		t.Fatal("inherited policy aliased")
	}
	off := &AIPolicyConfig{Enabled: false}
	merged = mergeModules(base, ModulesConfig{AI: AIConfig{Policy: off}})
	if merged.AI.Policy.Enabled {
		t.Fatal("explicit disabled policy ignored")
	}
}

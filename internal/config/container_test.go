package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestContainerStateRoundTrip(t *testing.T) {
	home := t.TempDir()
	in := &UserState{Name: "T", Profile: "full"}
	in.Modules.Container = UserContainerState{Enabled: true, Backend: "docker", DNS: []string{"1.1.1.1", "1.0.0.1"}}
	if err := SaveStateForHome(home, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadStateForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Modules.Container, in.Modules.Container) {
		t.Fatalf("round trip = %+v, want %+v", out.Modules.Container, in.Modules.Container)
	}

	for _, profile := range AvailableProfiles() {
		cfg, err := Load(profile, "", &SystemInfo{OS: "linux"})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.IsModuleEnabled("container") {
			t.Fatalf("profile %s enables container; it is opt-in", profile)
		}
		ApplyStateToConfig(cfg, out)
		if !cfg.IsModuleEnabled("container") || cfg.Modules.Container.Backend != "docker" ||
			!reflect.DeepEqual(cfg.Modules.Container.DNS, in.Modules.Container.DNS) {
			t.Fatalf("profile %s with state = %+v", profile, cfg.Modules.Container)
		}
		if cfg.TemplateData(home)["EnableContainer"] != true {
			t.Fatalf("profile %s: EnableContainer not published to templates", profile)
		}
	}
}

func TestValidate_Container(t *testing.T) {
	for _, tc := range []struct {
		c   UserContainerState
		err string
	}{
		{UserContainerState{Backend: "auto", DNS: []string{"1.1.1.1", "2606:4700:4700::1111"}}, ""},
		{UserContainerState{Backend: "lima"}, "modules.container.backend"},
		{UserContainerState{DNS: []string{"one.one.one.one"}}, "not an IP address"},
	} {
		s := &UserState{}
		s.Modules.Container = tc.c
		err := s.Validate()
		if (err == nil) != (tc.err == "") || (err != nil && !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("Validate(%+v) = %v, want %q", tc.c, err, tc.err)
		}
	}
}

// State overlays the profile or --config block field by field: an opt-in
// written by setup must not wipe a --config backend or DNS list.
func TestContainerStateOverlaysFieldByField(t *testing.T) {
	cfg := &Config{Modules: ModulesConfig{Container: ContainerConfig{Enabled: true, Backend: "podman", DNS: []string{"9.9.9.9"}}}}
	state := &UserState{}
	state.Modules.Container.Enabled = true
	ApplyStateToConfig(cfg, state)
	if c := cfg.Modules.Container; !c.Enabled || c.Backend != "podman" || !reflect.DeepEqual(c.DNS, []string{"9.9.9.9"}) {
		t.Fatalf("overlay = %+v, want the --config backend and DNS kept", c)
	}
}

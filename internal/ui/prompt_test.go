package ui

import (
	"slices"
	"testing"
)

func TestUnattendedPromptDefaults(t *testing.T) {
	t.Run("confirm accepts", func(t *testing.T) {
		got, err := Confirm("Continue?", true)
		if err != nil || !got {
			t.Fatalf("Confirm() = (%v, %v), want (true, nil)", got, err)
		}
	})

	t.Run("select returns default", func(t *testing.T) {
		got, err := Select("Profile", []string{"minimal", "full"}, "full", true)
		if err != nil || got != "full" {
			t.Fatalf("Select() = (%q, %v), want (%q, nil)", got, err, "full")
		}
	})

	t.Run("input returns default", func(t *testing.T) {
		got, err := Input("Name", "Young Joon Lee", true)
		if err != nil || got != "Young Joon Lee" {
			t.Fatalf("Input() = (%q, %v), want (%q, nil)", got, err, "Young Joon Lee")
		}
	})

	t.Run("detected input returns default", func(t *testing.T) {
		got, err := InputWithDetected("Terminal", "orca", true, true)
		if err != nil || got != "orca" {
			t.Fatalf("InputWithDetected() = (%q, %v), want (%q, nil)", got, err, "orca")
		}
	})

	t.Run("multi-select returns defaults", func(t *testing.T) {
		want := []string{"shell", "git"}
		got, err := MultiSelect("Modules", []string{"shell", "git", "ai"}, want, true)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("MultiSelect() = (%v, %v), want (%v, nil)", got, err, want)
		}
	})

	t.Run("labeled multi-select returns defaults", func(t *testing.T) {
		want := []string{"orca"}
		options := []SelectOption{{Label: "Orca", Value: "orca"}, {Label: "Warp", Value: "warp"}}
		got, err := MultiSelectLabeled("Terminals", options, want, true)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("MultiSelectLabeled() = (%v, %v), want (%v, nil)", got, err, want)
		}
	})

	t.Run("boolean confirm returns default", func(t *testing.T) {
		got, err := ConfirmBool("Enable AI?", false, true)
		if err != nil || got {
			t.Fatalf("ConfirmBool() = (%v, %v), want (false, nil)", got, err)
		}
	})
}

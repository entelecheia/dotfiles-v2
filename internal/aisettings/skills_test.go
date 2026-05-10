package aisettings

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestScanSkillsValidLegacyInvalidAndDuplicate(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".codex", "skills")
	mustWrite(t, filepath.Join(root, "valid", "SKILL.md"), []byte(`---
name: writer
description: Writes things
triggers:
  - write
allowed-tools:
  - shell
version: 1
schema_version: v1
---
# Writer
`))
	mustWrite(t, filepath.Join(root, "dupe", "SKILL.md"), []byte(`---
name: writer
description: Duplicate
schema_version: v1
---
# Duplicate
`))
	mustWrite(t, filepath.Join(root, "legacy", "SKILL.md"), []byte(`---
name: old-writer
description: Missing schema version
---
# Legacy
`))
	mustWrite(t, filepath.Join(root, "invalid", "SKILL.md"), []byte(`---
name: "Bad Name"
description: Broken
schema_version: v1
---
# Invalid
`))

	report, err := ScanSkills(SkillScanOptions{HomeDir: home, Tools: []string{"codex"}})
	if err != nil {
		t.Fatalf("ScanSkills: %v", err)
	}
	if report.Counts[SkillStatusValid] != 2 {
		t.Fatalf("valid count = %d, want 2", report.Counts[SkillStatusValid])
	}
	if report.Counts[SkillStatusLegacy] != 1 {
		t.Fatalf("legacy count = %d, want 1", report.Counts[SkillStatusLegacy])
	}
	if report.Counts[SkillStatusInvalid] != 1 {
		t.Fatalf("invalid count = %d, want 1", report.Counts[SkillStatusInvalid])
	}
	if len(report.Duplicates) != 1 || report.Duplicates[0].Name != "writer" {
		t.Fatalf("duplicates = %+v, want writer", report.Duplicates)
	}
	errs := report.ValidationErrors(false)
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "duplicate skill name") || !strings.Contains(joined, "name must match") {
		t.Fatalf("validation errors missing duplicate or invalid name:\n%s", joined)
	}
	if strings.Contains(joined, "old-writer") {
		t.Fatalf("non-strict validation should not fail legacy skill:\n%s", joined)
	}
	strictErrs := report.ValidationErrors(true)
	if !strings.Contains(strings.Join(strictErrs, "\n"), "old-writer") {
		t.Fatalf("strict validation should fail legacy skill, got:\n%s", strings.Join(strictErrs, "\n"))
	}
}

func TestScanSkillsCustomRootReplacesDefaults(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(home, "custom-skills")
	mustWrite(t, filepath.Join(custom, "greet", "SKILL.md"), []byte(`---
name: greet
description: Greets
schema_version: v1
---
`))
	mustWrite(t, filepath.Join(home, ".codex", "skills", "ignored", "SKILL.md"), []byte(`---
name: ignored
description: Should not be scanned
schema_version: v1
---
`))

	report, err := ScanSkills(SkillScanOptions{HomeDir: home, Roots: []string{custom}})
	if err != nil {
		t.Fatalf("ScanSkills custom: %v", err)
	}
	if len(report.Items) != 1 {
		t.Fatalf("items = %d, want 1: %+v", len(report.Items), report.Items)
	}
	if report.Items[0].Frontmatter.Name != "greet" || report.Items[0].Tool != "custom" {
		t.Fatalf("unexpected item: %+v", report.Items[0])
	}
}

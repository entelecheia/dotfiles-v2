package module

import (
	"context"
	"fmt"
	"path/filepath"
)

// SecretsModule checks secrets status and directs user to CLI subcommand.
type SecretsModule struct{}

func (m *SecretsModule) Name() string { return "secrets" }

func (m *SecretsModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change

	keyName := rc.Config.Modules.SSH.KeyName
	if keyName == "" {
		keyName = "id_ed25519"
	}

	sshKeyPath := filepath.Join(rc.HomeDir, ".ssh", keyName)
	if !rc.Runner.FileExists(sshKeyPath) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("SSH key missing: %s", sshKeyPath),
			Command:     "dotfiles secrets restore",
		})
	}

	secretsShell := filepath.Join(rc.HomeDir, ".config", "shell", "90-secrets.sh")
	if !rc.Runner.FileExists(secretsShell) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("secrets shell config missing: %s", secretsShell),
			Command:     "dotfiles secrets restore",
		})
	}

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

func (m *SecretsModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	msg := "secrets are managed via the CLI — run: dotfiles secrets restore"
	fmt.Printf("  ℹ %s: %s\n", m.Name(), msg)
	return &ApplyResult{Changed: false, Messages: []string{msg}}, nil
}

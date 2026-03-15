package module

import (
	"context"
	"fmt"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/template"
)

// Module is the interface that all dotfiles modules must implement.
type Module interface {
	Name() string
	Check(ctx context.Context, rc *RunContext) (*CheckResult, error)
	Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error)
}

// RunContext carries config and runtime options to all modules.
type RunContext struct {
	Config   *config.Config
	Runner   *exec.Runner
	Brew     *exec.Brew
	Template *template.Engine
	DryRun   bool
	Yes      bool
	HomeDir  string
}

// CheckResult holds the result of a module's Check operation.
type CheckResult struct {
	Satisfied bool
	Changes   []Change
}

// Change describes a single pending change.
type Change struct {
	Description string
	Command     string
}

// ApplyResult holds the result of a module's Apply operation.
type ApplyResult struct {
	Changed  bool
	Messages []string
}

// defaultOrder defines the static module execution order.
var defaultOrder = []string{
	"packages",
	"shell",
	"git",
	"ssh",
	"terminal",
	"tmux",
	"workspace",
	"ai-tools",
	"fonts",
	"conda",
	"gpg",
	"secrets",
}

// Registry manages module registration and resolution.
type Registry struct {
	modules map[string]Module
}

// NewRegistry creates a registry with all modules registered.
func NewRegistry() *Registry {
	r := &Registry{modules: make(map[string]Module)}
	r.Register(&PackagesModule{})
	r.Register(&ShellModule{})
	r.Register(&GitModule{})
	r.Register(&SSHModule{})
	r.Register(&TerminalModule{})
	r.Register(&TmuxModule{})
	r.Register(&WorkspaceModule{})
	r.Register(&AIToolsModule{})
	r.Register(&FontsModule{})
	r.Register(&CondaModule{})
	r.Register(&GPGModule{})
	r.Register(&SecretsModule{})
	return r
}

// Register adds or replaces a module.
func (r *Registry) Register(m Module) {
	r.modules[m.Name()] = m
}

// Resolve returns modules in execution order, filtered by config and --module flag.
func (r *Registry) Resolve(cfg *config.Config, filter []string) []Module {
	filterSet := make(map[string]bool, len(filter))
	for _, f := range filter {
		filterSet[f] = true
	}

	var result []Module
	for _, name := range defaultOrder {
		if !cfg.IsModuleEnabled(name) {
			continue
		}
		if len(filterSet) > 0 && !filterSet[name] {
			continue
		}
		if m, ok := r.modules[name]; ok {
			result = append(result, m)
		}
	}
	return result
}

// RunAll executes Check then Apply on each module in order.
func RunAll(ctx context.Context, modules []Module, rc *RunContext) error {
	var errors []string
	for _, m := range modules {
		check, err := m.Check(ctx, rc)
		if err != nil {
			fmt.Printf("  ⚠ %s: check error: %v\n", m.Name(), err)
			continue
		}
		if check.Satisfied {
			fmt.Printf("  ✓ %s: already satisfied\n", m.Name())
			continue
		}

		for _, c := range check.Changes {
			fmt.Printf("  → %s: %s\n", m.Name(), c.Description)
		}

		if rc.DryRun {
			continue
		}

		result, err := m.Apply(ctx, rc)
		if err != nil {
			fmt.Printf("  ✗ %s: %v\n", m.Name(), err)
			errors = append(errors, fmt.Sprintf("%s: %v", m.Name(), err))
			continue
		}
		if result.Changed {
			for _, msg := range result.Messages {
				fmt.Printf("  ✓ %s: %s\n", m.Name(), msg)
			}
		}
	}
	if len(errors) > 0 {
		return fmt.Errorf("%d module(s) failed: %v", len(errors), errors)
	}
	return nil
}

// CheckAll runs Check on each module and returns results.
func CheckAll(ctx context.Context, modules []Module, rc *RunContext) (map[string]*CheckResult, error) {
	results := make(map[string]*CheckResult, len(modules))
	for _, m := range modules {
		check, err := m.Check(ctx, rc)
		if err != nil {
			return nil, fmt.Errorf("module %s check: %w", m.Name(), err)
		}
		results[m.Name()] = check
	}
	return results, nil
}

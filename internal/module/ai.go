package module

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/aisettings"
)

// AIModule manages AI CLI/config helper shell configs and Claude settings.
type AIModule struct{}

func (m *AIModule) Name() string { return "ai" }

func (m *AIModule) managedFiles(rc *RunContext) []templatedFile {
	return []templatedFile{
		{
			templatePath: "shell/30-ai.sh.tmpl",
			destPath:     filepath.Join(rc.HomeDir, ".config", "shell", "30-ai.sh"),
			isTemplate:   true,
			perm:         0644,
		},
		{
			templatePath: "claude/settings.json.tmpl",
			destPath:     filepath.Join(rc.HomeDir, ".config", "claude", "settings.json"),
			isTemplate:   true,
			perm:         0644,
		},
	}
}

func (m *AIModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	changes, err := checkTemplatedFiles(rc, m.managedFiles(rc))
	if err != nil {
		return nil, err
	}

	legacy := filepath.Join(rc.HomeDir, ".config", "shell", "30-ai-tools.sh")
	if rc.Runner.FileExists(legacy) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("remove legacy %s", legacy),
			Command:     fmt.Sprintf("rm %s", legacy),
		})
	}
	if rc.Config.Modules.AI.AgentsSSOT {
		manager := aisettings.NewAgentsManager(rc.Runner, rc.HomeDir)
		statuses, err := manager.Status()
		if err != nil {
			return nil, fmt.Errorf("agents SSOT status: %w", err)
		}
		applySet := make(map[string]bool)
		for _, id := range manager.DefaultApplyTools() {
			applySet[id] = true
		}
		for _, st := range statuses {
			if !applySet[st.Tool.ID] || st.Drift == "in-sync" {
				continue
			}
			changes = append(changes, Change{
				Description: fmt.Sprintf("reapply agents SSOT to %s (%s)", st.Tool.ID, st.Drift),
				Command:     fmt.Sprintf("dot ai agents apply --tool %s", st.Tool.ID),
			})
		}
	}
	if rc.Config.Modules.AI.HUD {
		manager := aisettings.NewHUDManager(rc.Runner, rc.HomeDir)
		statuses, err := manager.Status(nil)
		if err != nil {
			return nil, fmt.Errorf("AI HUD status: %w", err)
		}
		for _, st := range statuses {
			if st.Drift == "in-sync" {
				continue
			}
			changes = append(changes, Change{
				Description: fmt.Sprintf("apply AI HUD to %s (%s)", st.ToolID, st.Drift),
				Command:     fmt.Sprintf("dot ai hud apply --tool %s", st.ToolID),
			})
		}
	}
	// modules.ai.skills is intentionally not checked: runtime skill symlinks
	// are owned by the Maru app; dot only offers read-only diagnostics via
	// `dot ai skills status`.
	if mode := rc.Config.Modules.Git.CoauthorGuard; mode != "" && mode != aisettings.CoauthorGuardOff {
		manager := aisettings.NewCoauthorGuardManager(rc.Runner, rc.HomeDir)
		status, err := manager.Status(mode)
		if err != nil {
			return nil, fmt.Errorf("coauthor guard status: %w", err)
		}
		if status.AgentsDrift != "in-sync" {
			changes = append(changes, Change{
				Description: fmt.Sprintf("apply coauthor guard AGENTS instruction (%s)", status.AgentsDrift),
				Command:     "dot ai coauthor-guard apply",
			})
		}
	}

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

func (m *AIModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	var messages []string

	fileMessages, err := applyTemplatedFiles(rc, m.managedFiles(rc))
	if err != nil {
		return nil, err
	}
	messages = append(messages, fileMessages...)

	legacy := filepath.Join(rc.HomeDir, ".config", "shell", "30-ai-tools.sh")
	if rc.Runner.FileExists(legacy) {
		if err := rc.Runner.Remove(legacy); err != nil {
			return nil, fmt.Errorf("removing legacy %s: %w", legacy, err)
		}
		messages = append(messages, fmt.Sprintf("removed legacy %s", legacy))
	}
	if rc.Config.Modules.AI.AgentsSSOT {
		manager := aisettings.NewAgentsManager(rc.Runner, rc.HomeDir)
		ssotMissing := !rc.Runner.FileExists(manager.SSOTPath())
		if ssotMissing {
			// Fresh machines enable agents_ssot by default, so the first apply
			// must scaffold the SSOT before rendering it to tool targets.
			if _, err := manager.Init(aisettings.InitOptions{}); err != nil {
				return nil, fmt.Errorf("scaffolding agents SSOT: %w", err)
			}
			messages = append(messages, fmt.Sprintf("scaffolded agents SSOT %s", manager.SSOTPath()))
		}
		if ssotMissing && rc.DryRun {
			// Dry-run writes nothing, so there is no SSOT to render yet.
			messages = append(messages, "dry-run: agents SSOT apply deferred until the SSOT is scaffolded")
		} else {
			result, err := manager.Apply(aisettings.ApplyOptions{Tools: manager.DefaultApplyTools(), DryRun: rc.DryRun})
			if err != nil {
				return nil, fmt.Errorf("applying agents SSOT: %w", err)
			}
			for _, item := range result.Items {
				if item.Changed {
					messages = append(messages, fmt.Sprintf("applied agents SSOT to %s", item.TargetPath))
				}
			}
			messages = append(messages, result.Warnings...)
		}
	}
	if rc.Config.Modules.AI.HUD {
		manager := aisettings.NewHUDManager(rc.Runner, rc.HomeDir)
		result, err := manager.Apply(aisettings.HUDOptions{DryRun: rc.DryRun})
		if err != nil {
			return nil, fmt.Errorf("applying AI HUD: %w", err)
		}
		for _, item := range result.Items {
			if item.Changed {
				messages = append(messages, fmt.Sprintf("applied AI HUD to %s", item.ToolID))
			}
		}
	}
	if mode := rc.Config.Modules.Git.CoauthorGuard; mode != "" && mode != aisettings.CoauthorGuardOff {
		manager := aisettings.NewCoauthorGuardManager(rc.Runner, rc.HomeDir)
		result, err := manager.Apply(aisettings.CoauthorGuardOptions{Mode: mode, DryRun: rc.DryRun, ApplyAgents: rc.Config.Modules.AI.AgentsSSOT})
		if err != nil {
			return nil, fmt.Errorf("applying coauthor guard AGENTS instruction: %w", err)
		}
		if result.AgentsChanged {
			messages = append(messages, "applied coauthor guard AGENTS instruction")
		}
		if result.AgentsApplied {
			messages = append(messages, "reapplied agents SSOT after coauthor guard update")
		}
	}

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}

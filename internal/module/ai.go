package module

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/aisettings"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// AIModule manages AI CLI/config helper shell configs and Claude settings.
type AIModule struct{}

func (m *AIModule) Name() string { return "ai" }

func (m *AIModule) managedFiles(rc *RunContext) []shellFile {
	return []shellFile{
		{
			templatePath: "shell/30-ai.sh.tmpl",
			destPath:     filepath.Join(rc.HomeDir, ".config", "shell", "30-ai.sh"),
			isTemplate:   true,
		},
		{
			templatePath: "claude/settings.json.tmpl",
			destPath:     filepath.Join(rc.HomeDir, ".config", "claude", "settings.json"),
			isTemplate:   true,
		},
	}
}

func (m *AIModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change
	data := rc.Config.TemplateData()

	for _, f := range m.managedFiles(rc) {
		content, err := rc.Template.Render(f.templatePath, data)
		if err != nil {
			return nil, fmt.Errorf("rendering %s: %w", f.templatePath, err)
		}
		if fileutil.NeedsUpdate(rc.Runner, f.destPath, content) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("write %s", f.destPath),
				Command:     fmt.Sprintf("render %s -> %s", f.templatePath, f.destPath),
			})
		}
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
	if rc.Config.Modules.AI.Skills.Enabled {
		manager := aisettings.NewSkillsManager(rc.Runner, rc.HomeDir)
		status, err := manager.Status(aisettings.SkillsOptions{
			Provider: rc.Config.Modules.AI.Skills.Provider,
			SSOTPath: rc.Config.Modules.AI.Skills.SSOTPath,
			Tools:    rc.Config.Modules.AI.Skills.Tools,
		})
		if err != nil {
			return nil, fmt.Errorf("AI skills status: %w", err)
		}
		for _, item := range status.Items {
			if item.Status == aisettings.SkillLinkStatusInSync || item.Status == aisettings.SkillLinkStatusSourceMissing {
				continue
			}
			changes = append(changes, Change{
				Description: fmt.Sprintf("apply skills SSOT to %s/%s (%s)", item.ToolID, item.SkillName, item.Status),
				Command:     fmt.Sprintf("dot ai skills apply --tool %s", item.ToolID),
			})
		}
	}
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
	data := rc.Config.TemplateData()

	for _, f := range m.managedFiles(rc) {
		content, err := rc.Template.Render(f.templatePath, data)
		if err != nil {
			return nil, fmt.Errorf("rendering %s: %w", f.templatePath, err)
		}
		written, err := fileutil.EnsureFile(rc.Runner, f.destPath, content, 0644)
		if err != nil {
			return nil, fmt.Errorf("writing %s: %w", f.destPath, err)
		}
		if written {
			messages = append(messages, fmt.Sprintf("wrote %s", f.destPath))
		}
	}
	legacy := filepath.Join(rc.HomeDir, ".config", "shell", "30-ai-tools.sh")
	if rc.Runner.FileExists(legacy) {
		if err := rc.Runner.Remove(legacy); err != nil {
			return nil, fmt.Errorf("removing legacy %s: %w", legacy, err)
		}
		messages = append(messages, fmt.Sprintf("removed legacy %s", legacy))
	}
	if rc.Config.Modules.AI.AgentsSSOT {
		manager := aisettings.NewAgentsManager(rc.Runner, rc.HomeDir)
		result, err := manager.Apply(aisettings.ApplyOptions{Tools: manager.DefaultApplyTools(), DryRun: rc.DryRun})
		if err != nil {
			return nil, fmt.Errorf("applying agents SSOT: %w", err)
		}
		for _, item := range result.Items {
			if item.Changed {
				messages = append(messages, fmt.Sprintf("applied agents SSOT to %s", item.TargetPath))
			}
		}
		for _, warning := range result.Warnings {
			messages = append(messages, warning)
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
	if rc.Config.Modules.AI.Skills.Enabled {
		manager := aisettings.NewSkillsManager(rc.Runner, rc.HomeDir)
		result, err := manager.Apply(aisettings.SkillsOptions{
			Provider: rc.Config.Modules.AI.Skills.Provider,
			SSOTPath: rc.Config.Modules.AI.Skills.SSOTPath,
			Tools:    rc.Config.Modules.AI.Skills.Tools,
			DryRun:   rc.DryRun,
		})
		if err != nil {
			return nil, fmt.Errorf("applying AI skills: %w", err)
		}
		for _, item := range result.Items {
			if item.Changed {
				messages = append(messages, fmt.Sprintf("applied skills SSOT to %s", item.TargetPath))
			}
		}
		for _, warning := range result.Warnings {
			messages = append(messages, warning)
		}
		if warning := m.anchorDoctorWarning(ctx, rc); warning != "" {
			messages = append(messages, warning)
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

func (m *AIModule) anchorDoctorWarning(ctx context.Context, rc *RunContext) string {
	skills := rc.Config.Modules.AI.Skills
	if !skills.Enabled || strings.ToLower(strings.TrimSpace(skills.Provider)) != aisettings.SkillsProviderAnchor {
		return ""
	}
	if rc.Runner == nil || !rc.Runner.CommandExists("anchor") {
		return ""
	}
	if _, err := rc.Runner.RunQuery(ctx, "anchor", "doctor", "--help"); err != nil {
		return ""
	}
	result, err := rc.Runner.RunQuery(ctx, "anchor", "doctor", "--quiet")
	if err == nil {
		return ""
	}
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.Stdout)
	}
	if detail == "" {
		detail = "anchor doctor --quiet exited non-zero"
	}
	return "warning: anchor doctor reported critical skill issue(s): " + oneLine(detail)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

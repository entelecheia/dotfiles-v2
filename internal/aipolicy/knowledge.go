package aipolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// KnowledgeApproval is a narrow native MCP tool grant, never a server definition.
// Both consumers and launch adapters validate against the fixed tool allowlist.
type KnowledgeApproval struct {
	Server       string `json:"server"`
	Plugin       string `json:"plugin,omitempty"`
	Tool         string `json:"tool"`
	ApprovalMode string `json:"approval_mode"`
}

var vaultKnowledgeTools = []string{"read_note", "read_multiple_notes", "search_notes", "list_directory", "get_notes_info", "get_frontmatter", "get_vault_stats", "list_all_tags", "write_note", "patch_note", "update_frontmatter", "manage_tags"}
var memoryKnowledgeTools = []string{"search", "timeline", "get_observations", "get_tool_uses", "session_start_context", "smart_search", "smart_outline", "smart_unfold", "important_workflow", "list_corpora", "query_corpus", "observation_add", "observation_record_event", "observation_search", "observation_context", "observation_generation_status"}

// KnowledgeApprovals recognizes existing Codex bindings without copying identity
// or credentials. Claude permission patterns themselves do not register servers.
// Memory recording tools are available only when the installed plugin exposes
// its server runtime; granting them does not activate or change that runtime.
func KnowledgeApprovals(runtime Runtime) ([]KnowledgeApproval, error) {
	out := []KnowledgeApproval{}
	add := func(server, plugin string, names []string) {
		for _, name := range names {
			out = append(out, KnowledgeApproval{Server: server, Plugin: plugin, Tool: name, ApprovalMode: "approve"})
		}
	}
	switch runtime.Agent {
	case "claude":
		add("obsidian", "", vaultKnowledgeTools)
		add("plugin_claude-mem_mcp-search", "", memoryKnowledgeTools)
	case "codex":
		if runtime.Home == "" {
			return out, nil
		}
		home, homeErr := canonicalDeclaredHome(runtime.Home)
		if homeErr != nil {
			return nil, homeErr
		}
		path := filepath.Join(home, "config.toml")
		if err := safePreferencePath(path); err != nil {
			return nil, err
		}
		file, err := os.Open(path)
		if os.IsNotExist(err) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
		if err != nil {
			return nil, err
		}
		if len(data) > 1024*1024 {
			return nil, fmt.Errorf("Codex config too large for knowledge binding inspection")
		}
		// This is a conservative recognizer of known table spellings, not a TOML
		// rewriter. Multiline strings can impersonate table headers, so refuse them.
		if strings.Contains(string(data), "\"\"\"") || strings.Contains(string(data), "'''") {
			return nil, fmt.Errorf("multiline Codex config requires manual knowledge binding validation")
		}
		section := ""
		obsidian := false
		disabled := false
		pluginEnabled := false
		pluginServer := false
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "[") {
				end := strings.Index(line, "]")
				if end < 0 {
					return nil, fmt.Errorf("invalid Codex config table")
				}
				section = strings.TrimSpace(line[1:end])
				section = knowledgeTable(section)
				if strings.HasPrefix(section, "plugins.claude-mem@claude-mem-local.mcp_servers.mcp-search.") || section == "plugins.claude-mem@claude-mem-local.mcp_servers.mcp-search" {
					pluginServer = true
				}
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(strings.SplitN(value, "#", 2)[0])
			if section == "mcp_servers.obsidian" {
				if key == "enabled" && value == "false" {
					disabled = true
				}
				if key == "command" || key == "url" {
					if len(value) > 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
						obsidian = true
					}
				}
			}
			if section == "plugins.claude-mem@claude-mem-local" && key == "enabled" && value == "true" {
				pluginEnabled = true
			}
		}
		if obsidian && !disabled {
			add("obsidian", "", vaultKnowledgeTools)
		}
		if pluginEnabled && !pluginServer {
			known, validationErr := installedMemoryServer(runtime)
			if validationErr != nil {
				return nil, validationErr
			}
			if !known {
				return nil, fmt.Errorf("enabled claude-mem plugin binding is unknown; inspect native installed plugin metadata")
			}
			pluginServer = true
		}
		if pluginEnabled && pluginServer {
			add("mcp-search", "claude-mem@claude-mem-local", memoryKnowledgeTools)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return a.Plugin+"/"+a.Server+"/"+a.Tool < b.Plugin+"/"+b.Server+"/"+b.Tool
	})
	return out, nil
}

func knowledgeTools(agent string, grant KnowledgeApproval) []string {
	if grant.ApprovalMode != "approve" {
		return nil
	}
	if grant.Server == "obsidian" && grant.Plugin == "" {
		return vaultKnowledgeTools
	}
	if agent == "claude" && grant.Server == "plugin_claude-mem_mcp-search" && grant.Plugin == "" {
		return memoryKnowledgeTools
	}
	if agent == "codex" && grant.Server == "mcp-search" && grant.Plugin == "claude-mem@claude-mem-local" {
		return memoryKnowledgeTools
	}
	return nil
}
func knowledgeKey(grant KnowledgeApproval) string {
	prefix := "mcp_servers."
	if grant.Plugin != "" {
		prefix = "plugins." + strconv.Quote(grant.Plugin) + ".mcp_servers."
	}
	return prefix + strconv.Quote(grant.Server) + ".tools." + strconv.Quote(grant.Tool) + ".approval_mode"
}

// Codex -c splits raw dotted key segments and does not decode TOML key quotes.
// Callers validate all segments against the fixed allowlist before using this.
func knowledgeCLIKey(grant KnowledgeApproval) string {
	prefix := "mcp_servers."
	if grant.Plugin != "" {
		prefix = "plugins." + grant.Plugin + ".mcp_servers."
	}
	return prefix + grant.Server + ".tools." + grant.Tool + ".approval_mode"
}

// KnowledgeArgs emits only permission leaves, preserving native MCP identity.
// It never accepts wildcard grants, delete/move operations or corpus-build tools.
func KnowledgeArgs(runtime Runtime, grants []KnowledgeApproval) ([]string, error) {
	args := []string{}
	patterns := []string{}
	if runtime.Agent != "claude" && runtime.Agent != "codex" {
		if len(grants) > 0 {
			return nil, fmt.Errorf("knowledge grants unsupported for %s", runtime.Agent)
		}
		return args, nil
	}
	for _, grant := range grants {
		allowed := false
		for _, name := range knowledgeTools(runtime.Agent, grant) {
			if name == grant.Tool {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("unsupported knowledge tool grant")
		}
		if runtime.Agent == "claude" {
			patterns = append(patterns, "mcp__"+grant.Server+"__"+grant.Tool)
		} else {
			args = append(args, "-c", knowledgeCLIKey(grant)+"=\"approve\"")
		}
	}
	if len(patterns) > 0 {
		args = append(args, "--allowedTools", strings.Join(patterns, ","))
	}
	return args, nil
}

// knowledgeTable accepts conventional dotted keys while keeping quoted dotted
// names distinct from nested tables. Unsupported escaping never grants a tool.
func knowledgeTable(header string) string {
	parts := []string{}
	for len(strings.TrimSpace(header)) > 0 {
		header = strings.TrimSpace(header)
		token := ""
		if header[0] == '"' || header[0] == '\'' {
			quote := header[0]
			end := strings.IndexByte(header[1:], quote)
			if end < 0 {
				return ""
			}
			end++
			token = header[1:end]
			header = header[end+1:]
			if strings.ContainsAny(token, ".\\") {
				return ""
			}
		} else {
			end := strings.IndexByte(header, '.')
			if end < 0 {
				token = header
				header = ""
			} else {
				token = header[:end]
				header = header[end:]
			}
			token = strings.TrimSpace(token)
			for _, ch := range token {
				if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
					return ""
				}
			}
		}
		if token == "" {
			return ""
		}
		parts = append(parts, token)
		header = strings.TrimSpace(header)
		if header == "" {
			break
		}
		if header[0] != '.' {
			return ""
		}
		header = header[1:]
		if strings.TrimSpace(header) == "" {
			return ""
		}
	}
	return strings.Join(parts, ".")
}

// installedMemoryServer uses the native installed-version registry, never a
// recursive cache search. Only that exact cached manifest and MCP file are read.
func installedMemoryServer(runtime Runtime) (bool, error) {
	if runtime.Executable == "" {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, runtime.Executable, "plugin", "list", "--json", "--marketplace", "claude-mem-local")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+runtime.Home)
	output := &knowledgeOutput{}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("native claude-mem binding inspection failed")
	}
	var registry struct {
		Installed []struct {
			PluginID  string `json:"pluginId"`
			Version   string
			Installed bool
		}
	}
	if err := json.Unmarshal(output.Bytes(), &registry); err != nil {
		return false, fmt.Errorf("invalid native installed-plugin registry")
	}
	version := ""
	for _, plugin := range registry.Installed {
		if plugin.PluginID == "claude-mem@claude-mem-local" && plugin.Installed {
			version = plugin.Version
			break
		}
	}
	if version == "" || filepath.Base(version) != version || version == "." || version == ".." {
		return false, nil
	}
	home, err := canonicalDeclaredHome(runtime.Home)
	if err != nil {
		return false, err
	}
	// Native plugin trees may intentionally be shared by a symlink (Orca).
	// Resolve this read-only boundary once, then confine all metadata reads.
	rootPath, err := filepath.EvalSymlinks(filepath.Join(home, "plugins", "cache", "claude-mem-local", "claude-mem", version))
	if err != nil {
		return false, fmt.Errorf("installed claude-mem cache unavailable")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return false, err
	}
	defer root.Close()
	data, err := knowledgeReadJSON(root, filepath.Join(".codex-plugin", "plugin.json"))
	if err != nil {
		return false, fmt.Errorf("installed claude-mem manifest unavailable")
	}
	var manifest struct {
		MCPServers json.RawMessage `json:"mcpServers"`
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		return false, fmt.Errorf("invalid installed claude-mem manifest")
	}
	var relative string
	if err = json.Unmarshal(manifest.MCPServers, &relative); err != nil {
		return false, fmt.Errorf("unsupported claude-mem MCP manifest shape")
	}
	relative = filepath.Clean(relative)
	if !filepath.IsLocal(relative) {
		return false, fmt.Errorf("claude-mem MCP path escapes plugin")
	}
	data, err = knowledgeReadJSON(root, relative)
	if err != nil {
		return false, fmt.Errorf("installed claude-mem MCP definition unavailable")
	}
	var mcp struct {
		Servers map[string]struct {
			Command string
			URL     string
		} `json:"mcpServers"`
	}
	if err = json.Unmarshal(data, &mcp); err != nil {
		return false, fmt.Errorf("invalid claude-mem MCP definition")
	}
	server, ok := mcp.Servers["mcp-search"]
	return ok && (server.Command != "" || server.URL != ""), nil
}
func knowledgeReadJSON(root *os.Root, path string) ([]byte, error) {
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if len(data) > 1024*1024 {
		return nil, fmt.Errorf("plugin metadata exceeds inspection limit")
	}
	return data, err
}

type knowledgeOutput struct{ bytes.Buffer }

func (out *knowledgeOutput) Write(data []byte) (int, error) {
	if out.Len()+len(data) > 1024*1024 {
		return 0, fmt.Errorf("plugin registry exceeds inspection limit")
	}
	return out.Buffer.Write(data)
}

package appsettings

import (
	"sort"

	"github.com/entelecheia/dotfiles-v2/internal/config/catalog"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// Per-command entry points for the `dot apps` family. Each entry takes an
// Options struct of plain values and returns a typed result; wording,
// styling, prompting, and flag reading stay in internal/cli.

// installedCasks reports the installed cask set, or nil when Homebrew could
// not be queried at all. Callers distinguish "not installed" from "could not
// tell", so the nil map is meaningful and must not be normalized away.
func installedCasks(brew *exec.Brew) map[string]bool {
	if brew == nil || !brew.IsAvailable() {
		return nil
	}
	return brew.InstalledCasks()
}

// --- list ---

// ListOptions carries what `dot apps list` needs beyond the embedded
// catalog. Brew is nil when Homebrew cannot be queried (non-macOS host, or
// the binary is missing).
type ListOptions struct {
	Brew *exec.Brew
}

// ListApp is one catalog entry with its membership flags resolved.
type ListApp struct {
	Token       string
	Name        string
	Default     bool
	Recommended bool
	Installed   bool
}

// ListGroup mirrors one catalog group, in catalog order.
type ListGroup struct {
	Name string
	Apps []ListApp
}

// ListResult is the catalog with every entry's flags resolved.
type ListResult struct {
	Groups []ListGroup
}

// List loads the cask catalog and resolves the default/recommended/installed
// flags for every entry.
func List(opts ListOptions) (*ListResult, error) {
	cat, err := catalog.LoadMacApps()
	if err != nil {
		return nil, err
	}
	installed := installedCasks(opts.Brew)
	defaults := make(map[string]bool, len(cat.Defaults))
	for _, t := range cat.Defaults {
		defaults[t] = true
	}
	recommended := make(map[string]bool, len(cat.Recommended))
	for _, t := range cat.Recommended {
		recommended[t] = true
	}

	res := &ListResult{Groups: make([]ListGroup, 0, len(cat.Groups))}
	for _, g := range cat.Groups {
		lg := ListGroup{Name: g.Name, Apps: make([]ListApp, 0, len(g.Apps))}
		for _, a := range g.Apps {
			lg.Apps = append(lg.Apps, ListApp{
				Token:       a.Token,
				Name:        a.Name,
				Default:     defaults[a.Token],
				Recommended: recommended[a.Token],
				Installed:   installed != nil && installed[a.Token],
			})
		}
		res.Groups = append(res.Groups, lg)
	}
	return res, nil
}

// --- status ---

// StatusOptions carries what `dot apps status` needs beyond the Engine's own
// fields. Brew is nil when Homebrew cannot be queried.
type StatusOptions struct {
	Brew *exec.Brew
}

// StatusApp pairs one app's live/archive counts with its install state.
// InstallKnown is false when Homebrew could not be queried at all, which is
// a third state distinct from "installed" and "not installed".
type StatusApp struct {
	AppStatus
	Installed    bool
	InstallKnown bool
}

// StatusResult is the whole report, apps sorted by token.
type StatusResult struct {
	Hostname string
	HostRoot string
	Apps     []StatusApp
}

// StatusReport collects install and backup presence for every manifest app.
func (e *Engine) StatusReport(opts StatusOptions) *StatusResult {
	installed := installedCasks(opts.Brew)

	statuses := e.Status(nil)
	tokens := make([]string, 0, len(statuses))
	byToken := make(map[string]AppStatus, len(statuses))
	for _, s := range statuses {
		tokens = append(tokens, s.Token)
		byToken[s.Token] = s
	}
	sort.Strings(tokens)

	res := &StatusResult{
		Hostname: e.Hostname,
		HostRoot: e.HostRoot(),
		Apps:     make([]StatusApp, 0, len(tokens)),
	}
	for _, token := range tokens {
		res.Apps = append(res.Apps, StatusApp{
			AppStatus:    byToken[token],
			Installed:    installed[token],
			InstallKnown: installed != nil,
		})
	}
	return res
}

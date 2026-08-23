package aisettings

import "strings"

// This file holds the per-command entry points the cli layer calls: Options
// in, typed result out, no formatting. The archive flows (Backup, Restore,
// Export, Import) already had that shape and keep their home in
// aisettings.go next to the copy engine they drive.

// ManagedByAgents reports whether a manifest-relative path is a target the
// agents SSOT owns. Callers label such a path rather than treating it as an
// independently managed file, and the answer is derived from the agent tool
// registry, so a newly registered tool cannot leave a stale label behind.
func ManagedByAgents(path string) bool {
	if path == AgentsSSOTRelPath {
		return true
	}
	for _, tool := range RegisteredAgentTools() {
		target := strings.TrimPrefix(tool.TargetPath, "~/")
		if path == target {
			return true
		}
	}
	return false
}

// StatusOptions controls StatusReport.
type StatusOptions struct {
	// IncludeAuth adds the auth/local-secret manifest entries to the report.
	IncludeAuth bool
}

// StatusEntry is one manifest entry's live/backup presence plus whether the
// agents SSOT owns its path.
type StatusEntry struct {
	Status
	ManagedByAgents bool
}

// StatusResult is the whole dot ai status view.
type StatusResult struct {
	Hostname string
	HostRoot string
	// Latest is the resolved snapshot id. LatestKnown is false when no
	// snapshot resolves — a reportable state, not an error, which is why
	// StatusReport returns no error at all.
	Latest      string
	LatestKnown bool
	Entries     []StatusEntry
}

// StatusReport assembles the status view: host, backup root, latest
// snapshot, and per-entry live/backup presence.
func (e *Engine) StatusReport(opts StatusOptions) *StatusResult {
	res := &StatusResult{Hostname: e.Hostname, HostRoot: e.HostRoot()}
	if latest, err := e.ResolveLatest(); err == nil {
		res.Latest = latest
		res.LatestKnown = true
	}
	for _, st := range e.Status(opts.IncludeAuth) {
		res.Entries = append(res.Entries, StatusEntry{
			Status:          st,
			ManagedByAgents: ManagedByAgents(st.Entry.Path),
		})
	}
	return res
}

// PruneOptions controls PlanPrune and Prune.
type PruneOptions struct {
	// Keep is the number of most recent snapshots to retain.
	Keep int
}

// PrunePlan reports what a prune with these options would remove, so a
// caller can confirm before anything is deleted.
type PrunePlan struct {
	Total int
	// Keep is echoed back exactly as given, unclamped, because Delete is
	// computed from it; Prune itself floors Keep at 1.
	Keep     int
	Delete   int
	HostRoot string
}

// PlanPrune counts what Prune would remove without removing anything.
func (e *Engine) PlanPrune(opts PruneOptions) (*PrunePlan, error) {
	all, err := e.List()
	if err != nil {
		return nil, err
	}
	return &PrunePlan{
		Total:    len(all),
		Keep:     opts.Keep,
		Delete:   len(all) - opts.Keep,
		HostRoot: e.HostRoot(),
	}, nil
}

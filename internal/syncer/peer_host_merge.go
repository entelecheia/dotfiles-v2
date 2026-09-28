package syncer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// hotHostPaths are host files both Macs rewrite on their own (#181):
// Claude Code rewrites ~/.claude.json whenever it starts, and the skills
// sync rewrites its manifests. Newest-wins then drops whatever only the
// losing copy had.
var hotHostPaths = []string{".claude.json", ".claude/skills/synced/*/manifest.json"}

// defaultHotKeys are the top-level keys compared for a hot JSON file with no
// host_merge policy: losing one of these entries loses an MCP server or a
// project's settings.
var defaultHotKeys = []string{"mcpServers", "projects"}

func isHotHostPath(rel string) bool {
	for _, pattern := range hotHostPaths {
		if ok, _ := path.Match(pattern, rel); ok {
			return true
		}
	}
	return false
}

// keyDiff is one configured key's difference between the two copies.
type keyDiff struct {
	key                 string
	onlyLocal, onlyPeer []string
	changed             []string
}

func (d keyDiff) String() string {
	var parts []string
	if len(d.onlyPeer) > 0 {
		parts = append(parts, "peer only "+strings.Join(d.onlyPeer, ", "))
	}
	if len(d.onlyLocal) > 0 {
		parts = append(parts, "here only "+strings.Join(d.onlyLocal, ", "))
	}
	if len(d.changed) > 0 {
		parts = append(parts, "differ "+strings.Join(d.changed, ", "))
	}
	return d.key + ": " + strings.Join(parts, "; ")
}

func decodeJSONObject(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // keep large integers exact
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// diffJSONKeys compares the entries of each key's object value. A key whose
// value is not an object on both sides is compared whole.
func diffJSONKeys(local, peer map[string]any, keys []string) []keyDiff {
	var out []keyDiff
	for _, key := range keys {
		l, lok := local[key]
		r, rok := peer[key]
		d := keyDiff{key: key}
		lm, lIsObj := l.(map[string]any)
		rm, rIsObj := r.(map[string]any)
		switch {
		case !lok && !rok:
			continue
		case lIsObj && rIsObj:
			for name, lv := range lm {
				rv, ok := rm[name]
				switch {
				case !ok:
					d.onlyLocal = append(d.onlyLocal, name)
				case !jsonEqual(lv, rv):
					d.changed = append(d.changed, name)
				}
			}
			for name := range rm {
				if _, ok := lm[name]; !ok {
					d.onlyPeer = append(d.onlyPeer, name)
				}
			}
		case !lok:
			d.onlyPeer = []string{"(whole key)"}
		case !rok:
			d.onlyLocal = []string{"(whole key)"}
		case !jsonEqual(l, r):
			d.changed = []string{"(value)"}
		}
		if len(d.onlyLocal)+len(d.onlyPeer)+len(d.changed) == 0 {
			continue
		}
		sort.Strings(d.onlyLocal)
		sort.Strings(d.onlyPeer)
		sort.Strings(d.changed)
		out = append(out, d)
	}
	return out
}

func jsonEqual(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

// mergeJSONKeys merges the configured keys' object entries: every entry
// either copy has survives, and the newer copy wins an entry both have.
// Everything else is the newer copy's.
func mergeJSONKeys(newer, older map[string]any, keys []string) map[string]any {
	merged := make(map[string]any, len(newer))
	for k, v := range newer {
		merged[k] = v
	}
	for _, key := range keys {
		ov, ook := older[key]
		if !ook {
			continue
		}
		nv, nok := newer[key]
		if !nok {
			merged[key] = ov
			continue
		}
		nm, nIsObj := nv.(map[string]any)
		om, oIsObj := ov.(map[string]any)
		if !nIsObj || !oIsObj {
			continue
		}
		union := make(map[string]any, len(nm)+len(om))
		for name, v := range om {
			union[name] = v
		}
		for name, v := range nm {
			union[name] = v
		}
		merged[key] = union
	}
	return merged
}

func encodeJSONObject(obj map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(obj); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// readPeerHostFile reads a host file on the peer, relative to its home. A
// missing file is (nil, nil).
func readPeerHostFile(ctx context.Context, runner *exec.Runner, cfg *Config, rel string) ([]byte, error) {
	res, err := runner.Run(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", cfg.Target.Host,
		"if [ -f "+shellQuote(rel)+" ]; then cat -- "+shellQuote(rel)+"; else echo __dot_absent__ >&2; exit 3; fi")
	if err != nil {
		if res != nil && strings.Contains(res.Stderr, "__dot_absent__") {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s on %s: %w", rel, cfg.Target.Host, err)
	}
	return []byte(res.Stdout), nil
}

// hostKeyPolicy is the key list to compare or merge for a host file, and
// whether it is a merge policy (host_merge) rather than the hot-file default.
func hostKeyPolicy(cfg *Config, rel string) ([]string, bool) {
	if keys, ok := cfg.HostMerge[rel]; ok && len(keys) > 0 {
		return keys, true
	}
	if isHotHostPath(rel) && strings.HasSuffix(rel, ".json") {
		return defaultHotKeys, false
	}
	return nil, false
}

// annotateHotItems marks hot host items and, for JSON files with a key
// policy, lists the key-level differences between the two copies. Unless
// host_merge merges the file in this run (two-way runs, additive host
// paths only), a losing copy that holds entries the winner lacks is called
// out: newest-wins would drop them.
func annotateHotItems(ctx context.Context, probe *exec.Runner, cfg *Config, items []PeerPlanItem, twoWay bool) {
	for i := range items {
		it := &items[i]
		if it.Scope == PlanScopeWorkspace {
			continue
		}
		keys, merge := hostKeyPolicy(cfg, it.Path)
		it.Hot = isHotHostPath(it.Path) || merge
		if len(keys) == 0 {
			continue
		}
		localData, err := os.ReadFile(filepath.Join(cfg.HomeDir(), filepath.FromSlash(it.Path)))
		if err != nil {
			continue
		}
		peerData, err := readPeerHostFile(ctx, probe, cfg, it.Path)
		if err != nil || peerData == nil {
			continue
		}
		local, lerr := decodeJSONObject(localData)
		peer, perr := decodeJSONObject(peerData)
		if lerr != nil || perr != nil {
			continue
		}
		// The merge writes a file present on both sides whose copies differ.
		merging := merge && twoWay && it.Scope == PlanScopeHost && it.Action == "update"
		hint := "set host_merge for this file to keep them"
		switch {
		case merge && it.Scope != PlanScopeHost:
			hint = "host_merge does not apply to tracked host paths"
		case merge && !twoWay:
			hint = "host_merge runs only in a two-way sync"
		}
		var warnings []string
		for _, d := range diffJSONKeys(local, peer, keys) {
			it.Keys = append(it.Keys, d.String())
			// The copy this item overwrites loses what only it has.
			lost := d.onlyLocal
			if it.Direction == "push" {
				lost = d.onlyPeer
			}
			if len(lost) == 0 {
				continue
			}
			if !merging {
				warnings = append(warnings, fmt.Sprintf("%s entries %s exist only in the overwritten copy", d.key, strings.Join(lost, ", ")))
			}
		}
		if len(warnings) > 0 {
			it.Warning = "newest wins: " + strings.Join(warnings, "; ") + "; " + hint
		}
		if merging {
			// Both machines get the merged file; the pass then moves nothing.
			it.Action, it.Direction = "merge", "both"
			it.Reason = "merged on both machines before the transfer (host_merge: " + strings.Join(keys, ", ") + ")"
		}
	}
}

// mergePeerHostFiles applies the host_merge policies before the additive
// pass: for each listed file present on both machines with different
// content, the configured keys' entries of both copies are merged into the
// newer one, written here with a fresh mtime and pushed with it, so the
// additive pass then finds two equal copies. Both sides are always written:
// the result must not depend on which copy the pass would call newer (the
// peer's mtime is read to the second). A file under a tracked host entry is
// left to the tracked pass. It returns the files it merged.
//
// ponytail: known ceiling. See docs/CEILINGS.md (host_merge read-merge-write race).
func mergePeerHostFiles(ctx context.Context, runner, probe *exec.Runner, cfg *Config) ([]string, error) {
	tracked, err := readPeerHomeTrackedEntries(PeerHomeTrackedFile(cfg.LocalPaths))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	files := make([]string, 0, len(cfg.HostMerge))
	for rel := range cfg.HostMerge {
		if _, covered := filterUntrackedHomeEntries([]string{rel}, tracked); !covered {
			files = append(files, rel)
		}
	}
	sort.Strings(files)
	var merged []string
	for _, rel := range files {
		keys := cfg.HostMerge[rel]
		if len(keys) == 0 {
			continue
		}
		if validateTombstoneRel(rel) != nil || strings.HasPrefix(rel, "~") {
			return merged, fmt.Errorf("host_merge: %q must be a path relative to $HOME, like .claude.json", rel)
		}
		localPath := filepath.Join(cfg.HomeDir(), filepath.FromSlash(rel))
		info, err := os.Stat(localPath)
		if err != nil {
			continue // absent here: the pass copies the peer's
		}
		localData, err := os.ReadFile(localPath)
		if err != nil {
			return merged, err
		}
		peerData, err := readPeerHostFile(ctx, probe, cfg, rel)
		if err != nil {
			return merged, err
		}
		if peerData == nil {
			continue
		}
		local, lerr := decodeJSONObject(localData)
		peer, perr := decodeJSONObject(peerData)
		if lerr != nil || perr != nil {
			return merged, fmt.Errorf("host_merge %s: both copies must be JSON objects", rel)
		}
		peerMtime, err := peerHostMtime(ctx, probe, cfg, rel)
		if err != nil {
			return merged, err
		}
		newer, older := local, peer
		if peerMtime.After(info.ModTime()) {
			newer, older = peer, local
		}
		result := mergeJSONKeys(newer, older, keys)
		if jsonEqual(result, newer) && jsonEqual(result, older) {
			continue // identical in substance
		}
		body, err := encodeJSONObject(result)
		if err != nil {
			return merged, err
		}
		// Keeps the mode (~/.claude.json holds tokens, 0600) and owner, and
		// refuses a symlink rather than replacing it.
		if err := runner.WriteFileAtomic(localPath, body, 0o600); err != nil {
			return merged, fmt.Errorf("host_merge %s: %w", rel, err)
		}
		now := time.Now()
		if err := os.Chtimes(localPath, now, now); err != nil {
			return merged, err
		}
		args := []string{"-t", "-e", "ssh -o BatchMode=yes -o ConnectTimeout=5"}
		if cfg.RemoteRsyncPath != "" {
			args = append(args, "--rsync-path="+cfg.RemoteRsyncPath)
		}
		args = append(args, localPath, cfg.Target.Host+":"+rel)
		if _, err := runner.Run(ctx, cfg.rsyncBin(), args...); err != nil {
			return merged, fmt.Errorf("host_merge %s: pushing the merged copy: %w", rel, err)
		}
		merged = append(merged, rel)
	}
	return merged, nil
}

func peerHostMtime(ctx context.Context, runner *exec.Runner, cfg *Config, rel string) (time.Time, error) {
	res, err := runner.Run(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", cfg.Target.Host,
		"f="+shellQuote(rel)+"; if stat -c %Y \"$f\" >/dev/null 2>&1; then stat -c %Y \"$f\"; else stat -f %m \"$f\"; fi")
	if err != nil {
		return time.Time{}, fmt.Errorf("reading the mtime of %s on %s: %w", rel, cfg.Target.Host, err)
	}
	var sec int64
	if _, err := fmt.Sscan(strings.TrimSpace(res.Stdout), &sec); err != nil {
		return time.Time{}, fmt.Errorf("unexpected mtime %q for %s", strings.TrimSpace(res.Stdout), rel)
	}
	return time.Unix(sec, 0), nil
}

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
// policy, lists the key-level differences between the two copies. Without
// a merge policy, a losing copy that holds entries the winner lacks is
// called out: newest-wins would drop them.
func annotateHotItems(ctx context.Context, probe *exec.Runner, cfg *Config, items []PeerPlanItem) {
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
		for _, d := range diffJSONKeys(local, peer, keys) {
			it.Keys = append(it.Keys, d.String())
			if merge {
				continue
			}
			// The copy this item overwrites loses what only it has.
			lost := d.onlyLocal
			if it.Direction == "push" {
				lost = d.onlyPeer
			}
			if len(lost) > 0 {
				it.Warning = fmt.Sprintf("newest wins: %s entries %s exist only in the overwritten copy; set host_merge for this file to keep them", d.key, strings.Join(lost, ", "))
			}
		}
		if merge && len(it.Keys) > 0 {
			it.Reason = "merged before the transfer (host_merge: " + strings.Join(keys, ", ") + ")"
		}
	}
}

// mergePeerHostFiles applies the host_merge policies before the additive
// pass: for each listed file present on both machines, the configured keys'
// entries of both copies are merged into the newer one, written here with a
// fresh mtime and pushed with it, so the additive pass then finds two equal
// copies. A file whose newer copy already holds every entry is left to the
// pass. It returns the files it merged.
//
// ponytail: read-merge-write race. An app rewriting the file between the
// read and the write loses that rewrite; a lock the app honors would be the
// upgrade, and none exists for ~/.claude.json.
func mergePeerHostFiles(ctx context.Context, runner, probe *exec.Runner, cfg *Config) ([]string, error) {
	files := make([]string, 0, len(cfg.HostMerge))
	for rel := range cfg.HostMerge {
		files = append(files, rel)
	}
	sort.Strings(files)
	var merged []string
	for _, rel := range files {
		keys := cfg.HostMerge[rel]
		if len(keys) == 0 {
			continue
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
		if jsonEqual(result, newer) {
			continue // the newer copy holds everything; the pass copies it
		}
		body, err := encodeJSONObject(result)
		if err != nil {
			return merged, err
		}
		if err := atomicWrite(localPath, body); err != nil {
			return merged, fmt.Errorf("host_merge %s: %w", rel, err)
		}
		if err := os.Chmod(localPath, info.Mode().Perm()); err != nil {
			return merged, err
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
		if _, err := runner.Run(ctx, "rsync", args...); err != nil {
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

package cli

import (
	"bytes"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// completionDir is the on-disk location for installed shell completions.
// Lives under XDG data home so removal of dotfiles cleans it up too.
func completionDir(homeDir string) string {
	return filepath.Join(homeDir, ".local", "share", "dotfiles", "completions")
}

// installCompletions generates zsh + bash + fish completion scripts for
// root and writes them to <home>/.local/share/dotfiles/completions/.
//
// Cobra writes completions tied to the root command's Name (dotfiles).
// We post-process so both `dot` (alias) and `dotfiles` (canonical) are
// registered with the same handler — otherwise tab-completion would
// only fire for the long form.
//
// Files written (only rewritten when content changes):
//
//	_dot           zsh autoload-style; registers `compdef _dotfiles dot dotfiles`
//	dot.bash       bash, source-style; registers complete for both names
//	dot.fish       fish, best-effort
//
// The fpath integration that picks these up lives in 05-completion.sh.
// Returns true when any file changed (so the caller can report it).
func installCompletions(root *cobra.Command, runner *exec.Runner, homeDir string) (bool, error) {
	dir := completionDir(homeDir)
	if err := runner.MkdirAll(dir, 0755); err != nil {
		return false, fmt.Errorf("creating %s: %w", dir, err)
	}

	var changed bool

	// ── zsh ─────────────────────────────────────────────────────────────
	var zshBuf bytes.Buffer
	if err := root.GenZshCompletion(&zshBuf); err != nil {
		return changed, fmt.Errorf("generating zsh completion: %w", err)
	}
	zshScript := patchZshForAlias(zshBuf.Bytes(), "dot")
	zshFile := filepath.Join(dir, "_dot")
	written, err := fileutil.EnsureFile(runner, zshFile, zshScript, 0644)
	if err != nil {
		return changed, fmt.Errorf("writing %s: %w", zshFile, err)
	}
	if written {
		changed = true
	}

	// ── bash ────────────────────────────────────────────────────────────
	var bashBuf bytes.Buffer
	if err := root.GenBashCompletionV2(&bashBuf, true); err != nil {
		return changed, fmt.Errorf("generating bash completion: %w", err)
	}
	bashScript := patchBashForAlias(bashBuf.Bytes(), "dot")
	bashFile := filepath.Join(dir, "dot.bash")
	written, err = fileutil.EnsureFile(runner, bashFile, bashScript, 0644)
	if err != nil {
		return changed, fmt.Errorf("writing %s: %w", bashFile, err)
	}
	if written {
		changed = true
	}

	// ── fish (best-effort, write but don't fail apply if missing) ──────
	var fishBuf bytes.Buffer
	if err := root.GenFishCompletion(&fishBuf, true); err == nil {
		fishFile := filepath.Join(dir, "dot.fish")
		w, _ := fileutil.EnsureFile(runner, fishFile, fishBuf.Bytes(), 0644)
		if w {
			changed = true
		}
	}

	return changed, nil
}

// patchZshForAlias rewrites the `compdef _dotfiles dotfiles` directive
// so the same handler also fires for the alias name. Cobra's standard
// output binds only to root.Name(); without this, `dot <Tab>` does
// nothing while `dotfiles <Tab>` works.
func patchZshForAlias(script []byte, alias string) []byte {
	old := []byte("\ncompdef _dotfiles dotfiles\n")
	if !bytes.Contains(script, old) {
		// Cobra format changed; leave the script as-is rather than corrupt it.
		return script
	}
	replacement := []byte("\ncompdef _dotfiles " + alias + " dotfiles\n")
	out := bytes.Replace(script, old, replacement, 1)

	// Also fix the `#compdef dotfiles` header so both names are recognized
	// when the file is autoloaded standalone.
	headerOld := []byte("#compdef dotfiles\n")
	headerNew := []byte("#compdef " + alias + " dotfiles\n")
	out = bytes.Replace(out, headerOld, headerNew, 1)
	return out
}

// patchBashForAlias appends a second `complete` registration so the
// alias name fires the same handler. Cobra v2 bash output ends with
// the registration block for the canonical name only.
func patchBashForAlias(script []byte, alias string) []byte {
	addition := []byte(fmt.Sprintf(`
# Register handler for alias `+"`%s`"+` so it tab-completes the same as dotfiles.
if [[ $(type -t compopt) = "builtin" ]]; then
    complete -o default -F __start_dotfiles %s
else
    complete -o default -o nospace -F __start_dotfiles %s
fi
`, alias, alias, alias))

	// Insert before the trailing "ex:" mode-line so the file still
	// looks tidy; if the marker isn't there, just append.
	mode := []byte("# ex: ts=")
	idx := bytes.LastIndex(script, mode)
	if idx < 0 {
		return append(script, addition...)
	}
	out := make([]byte, 0, len(script)+len(addition))
	out = append(out, script[:idx]...)
	out = append(out, addition...)
	out = append(out, script[idx:]...)
	return out
}

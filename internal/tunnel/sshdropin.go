package tunnel

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const dropInHeader = "# Managed by 'dot tunnel client'. One Host block per entry.\n\n"

type SSHHostBlock struct {
	Host  string
	Lines []string
}

func DropInPath(home string) string {
	return filepath.Join(home, ".ssh", "config.d", "dot-tunnel")
}

func SSHConfigPath(home string) string {
	return filepath.Join(home, ".ssh", "config")
}

// AddHost appends a ProxyCommand block for hostname. The returned bool is
// false when the host was already configured and the file was left untouched.
func AddHost(home, hostname string) (bool, []string, error) {
	if err := ValidateHostname(hostname); err != nil {
		return false, nil, err
	}
	content, err := readDropIn(home)
	if err != nil {
		return false, nil, err
	}
	header, blocks := parseDropIn(content)
	for _, block := range blocks {
		if strings.EqualFold(block.Host, hostname) {
			return false, clientWarnings(home), nil
		}
	}
	blocks = append(blocks, SSHHostBlock{Host: hostname, Lines: renderHostBlock(hostname)})
	if err := writeDropIn(home, renderDropIn(header, blocks)); err != nil {
		return false, nil, err
	}
	return true, clientWarnings(home), nil
}

func ListHosts(home string) ([]string, error) {
	content, err := readDropIn(home)
	if err != nil {
		return nil, err
	}
	_, blocks := parseDropIn(content)
	hosts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		hosts = append(hosts, block.Host)
	}
	sort.Strings(hosts)
	return hosts, nil
}

func RemoveHost(home, hostname string) (bool, error) {
	if err := ValidateHostname(hostname); err != nil {
		return false, err
	}
	content, err := readDropIn(home)
	if err != nil {
		return false, err
	}
	header, blocks := parseDropIn(content)
	next := blocks[:0]
	removed := false
	for _, block := range blocks {
		if strings.EqualFold(block.Host, hostname) {
			removed = true
			continue
		}
		next = append(next, block)
	}
	if !removed {
		return false, nil
	}
	if err := writeDropIn(home, renderDropIn(header, next)); err != nil {
		return false, err
	}
	return true, nil
}

func readDropIn(home string) (string, error) {
	data, err := os.ReadFile(DropInPath(home))
	if os.IsNotExist(err) {
		return dropInHeader, nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func writeDropIn(home, content string) error {
	dir := filepath.Dir(DropInPath(home))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	path := DropInPath(home)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return os.Chmod(path, 0o600)
}

func parseDropIn(content string) (string, []SSHHostBlock) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	headerLines := []string{}
	var blocks []SSHHostBlock
	var current *SSHHostBlock

	flush := func() {
		if current != nil {
			blocks = append(blocks, *current)
			current = nil
		}
	}
	for _, line := range lines {
		// ssh_config keywords are case-insensitive and may be indented or
		// tab-separated — recognize every valid "Host <name>" spelling so
		// hand-edited blocks stay visible to list/remove.
		if fields := strings.Fields(line); len(fields) >= 2 && strings.EqualFold(fields[0], "host") {
			flush()
			current = &SSHHostBlock{Host: fields[1], Lines: []string{line}}
			continue
		}
		if current == nil {
			headerLines = append(headerLines, line)
			continue
		}
		current.Lines = append(current.Lines, line)
	}
	flush()

	header := strings.TrimRight(strings.Join(headerLines, "\n"), "\n")
	if strings.TrimSpace(header) == "" {
		header = strings.TrimRight(dropInHeader, "\n")
	}
	return header + "\n\n", blocks
}

func renderDropIn(header string, blocks []SSHHostBlock) string {
	var b strings.Builder
	b.WriteString(header)
	for i, block := range blocks {
		if i > 0 {
			b.WriteString("\n")
		}
		lines := trimTrailingBlankLines(block.Lines)
		for _, line := range lines {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func renderHostBlock(hostname string) []string {
	return []string{
		"Host " + hostname,
		"    ProxyCommand cloudflared access ssh --hostname %h",
		"",
	}
}

func trimTrailingBlankLines(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	out := append([]string(nil), lines[:end]...)
	out = append(out, "")
	return out
}

func clientWarnings(home string) []string {
	var warnings []string
	if _, err := exec.LookPath("cloudflared"); err != nil {
		warnings = append(warnings, "cloudflared not found in PATH; install it on this client before connecting")
	}
	data, err := os.ReadFile(SSHConfigPath(home))
	if err != nil || !includesConfigD(string(data)) {
		warnings = append(warnings, "ssh config does not include ~/.ssh/config.d/*; add that Include line if this host is not picked up")
	}
	return warnings
}

// includesConfigD reports whether any Include directive references config.d.
// ssh_config accepts several equivalent spellings ("Include ~/.ssh/config.d/*",
// "Include config.d/*" — relative paths resolve against ~/.ssh) so the check
// is per-line and tolerant instead of matching one literal string.
func includesConfigD(config string) bool {
	for _, line := range strings.Split(config, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "include") {
			continue
		}
		for _, arg := range fields[1:] {
			if strings.Contains(arg, "config.d") {
				return true
			}
		}
	}
	return false
}

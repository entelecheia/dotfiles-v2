package fileutil

import (
	"os"
	"strings"
)

// TailLog returns the last n lines of logFile as a single string.
// Canonical implementation shared by the rsync, gsync, and tunnel commands.
func TailLog(logFile string, n int) (string, error) {
	data, err := os.ReadFile(logFile)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}

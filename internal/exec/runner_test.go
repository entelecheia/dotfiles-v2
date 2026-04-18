package exec

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestRunInteractive_DryRunSkipsExecution verifies that dry-run turns
// RunInteractive into a no-op that warns via slog and returns nil without
// attempting to spawn the process. Without this guarantee a caller that
// passed a DryRun runner could still trigger an interactive OAuth prompt.
func TestRunInteractive_DryRunSkipsExecution(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewRunner(true, logger)

	// Use a command that would fail loudly if actually executed in dry-run:
	// /bin/false exits non-zero, and a real interactive spawn would print to
	// the test harness's stderr. Neither should happen.
	err := runner.RunInteractive(context.Background(), "/bin/false")
	if err != nil {
		t.Fatalf("dry-run should return nil, got %v", err)
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "dry-run: skipped interactive command") {
		t.Errorf("expected dry-run warning in log, got: %q", logOutput)
	}
	if !strings.Contains(logOutput, "level=WARN") {
		t.Errorf("expected WARN level, got: %q", logOutput)
	}
}

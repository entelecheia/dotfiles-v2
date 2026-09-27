package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestExecuteWithSignalsPreservesContext(t *testing.T) {
	type key struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "preserved"))
	defer cancel()
	c := &cobra.Command{Use: "probe", RunE: func(cmd *cobra.Command, _ []string) error {
		if cmd.Context().Value(key{}) != "preserved" {
			t.Fatal("caller context discarded")
		}
		cancel()
		select {
		case <-cmd.Context().Done():
			return cmd.Context().Err()
		case <-time.After(time.Second):
			t.Fatal("caller cancellation not propagated")
		}
		return nil
	}}
	c.SetContext(parent)
	c.SetArgs([]string{})
	c.SilenceErrors = true
	c.SilenceUsage = true
	if err := executeWithSignals(c); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestExecuteWithSignalsCancelsCommand(t *testing.T) {
	if mode := os.Getenv("DOT_TEST_SIGNAL_CHILD"); mode != "" {
		c := &cobra.Command{Use: "probe", RunE: func(cmd *cobra.Command, _ []string) error {
			sig := syscall.SIGINT
			if mode == "term" {
				sig = syscall.SIGTERM
			}
			if err := syscall.Kill(os.Getpid(), sig); err != nil {
				return err
			}
			select {
			case <-cmd.Context().Done():
				return nil
			case <-time.After(time.Second):
				return errors.New("signal did not cancel command context")
			}
		}}
		c.SetArgs([]string{})
		if err := executeWithSignals(c); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, mode := range []string{"int", "term"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecuteWithSignalsCancelsCommand$")
			child.Env = append(os.Environ(), "DOT_TEST_SIGNAL_CHILD="+mode)
			if out, err := child.CombinedOutput(); err != nil {
				t.Fatalf("signal terminated process before cleanup: %v\n%s", err, out)
			}
		})
	}
}

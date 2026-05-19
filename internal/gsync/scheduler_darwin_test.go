//go:build darwin

package gsync

import "testing"

func TestLaunchdPrintTarget(t *testing.T) {
	got := launchdPrintTarget(501, SchedulerKindPush.LaunchdLabel())
	want := "gui/501/com.dotfiles.gdrive-sync"
	if got != want {
		t.Fatalf("launchdPrintTarget = %q, want %q", got, want)
	}
}

func TestLaunchdStateFromPrintStatus(t *testing.T) {
	cases := []struct {
		name        string
		plistExists bool
		printOK     bool
		want        SchedulerState
	}{
		{"missing plist", false, true, SchedulerNotInstalled},
		{"loaded", true, true, SchedulerRunning},
		{"not loaded", true, false, SchedulerStopped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := launchdStateFromPrintStatus(tc.plistExists, tc.printOK)
			if got != tc.want {
				t.Fatalf("state = %s, want %s", got, tc.want)
			}
		})
	}
}

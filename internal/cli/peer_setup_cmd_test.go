package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/syncer"
)

func TestPrintPeerScheduleDryRunTargetUserGuidance(t *testing.T) {
	for _, off := range []bool{false, true} {
		t.Run(map[bool]string{false: "install", true: "off"}[off], func(t *testing.T) {
			var out, errOut bytes.Buffer
			result := &syncer.PeerScheduleResult{
				Off: off, DryRun: true, TargetUserActionRequired: true,
				Plist:    "/target/Library/LaunchAgents/com.dotfiles.peer.plist",
				Interval: 15 * time.Minute,
			}
			printPeerScheduleDryRun(&Printer{Out: &out, Err: &errOut}, result)

			verb := "write"
			if off {
				verb = "remove"
			}
			if !strings.Contains(out.String(), "dry-run: would "+verb+" "+result.Plist) {
				t.Errorf("stdout missing %s preview: %q", verb, out.String())
			}
			for _, want := range []string{"target-user action required", "no service-manager action ran in the caller domain", "sudo -iu <target-user>"} {
				if !strings.Contains(errOut.String(), want) {
					t.Errorf("stderr missing %q: %q", want, errOut.String())
				}
			}
		})
	}
}

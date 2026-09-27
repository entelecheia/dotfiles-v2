package cli

import "testing"

func TestValidateSyncPeerTarget(t *testing.T) {
	for _, ok := range []string{"user@mac2", "macbook-pro-2023", "user.name@host.internal", "192.168.0.10"} {
		if err := validateSyncPeerTarget(ok); err != nil {
			t.Errorf("valid target %q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-oProxyCommand=evil", "-F", "user @host", "user\thost", "user\nhost"} {
		if err := validateSyncPeerTarget(bad); err == nil {
			t.Errorf("hostile target %q accepted", bad)
		}
	}
}

package syncer

import (
	"testing"
)

func TestRejectGenericPeerProfile(t *testing.T) {
	if err := RejectGenericPeerProfile(&Config{Profile: PeerProfile}); err == nil {
		t.Fatal("generic sync commands must not bypass the peer tombstone transaction")
	}
	if err := RejectGenericPeerProfile(&Config{Profile: DefaultProfile}); err != nil {
		t.Fatalf("default sync profile rejected: %v", err)
	}
}

package secrets

import (
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/config"
)

func TestSSHKeyNameRejectsPathSeparators(t *testing.T) {
	for _, bad := range []string{"../evil", "a/b", "..", "."} {
		state := &config.UserState{}
		state.SSH.KeyName = bad
		if _, err := sshKeyName(state); err == nil {
			t.Errorf("sshKeyName(%q) should fail", bad)
		}
	}
	state := &config.UserState{}
	state.SSH.KeyName = "id_rsa"
	if name, err := sshKeyName(state); err != nil || name != "id_rsa" {
		t.Errorf("sshKeyName(id_rsa) = %q, %v", name, err)
	}
}

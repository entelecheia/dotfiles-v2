package module

import "context"

// ghAuthScopes lists OAuth scopes requested during gh auth login.
// Covers: private repo access, SSH/GPG key management, Actions workflows, org read.
var ghAuthScopes = []string{
	"repo",
	"read:org",
	"gist",
	"workflow",
	"admin:public_key",
	"admin:ssh_signing_key",
	"write:gpg_key",
}

// ghAuthenticated checks if gh CLI is authenticated to github.com.
func ghAuthenticated(rc *RunContext) bool {
	result, err := rc.Runner.Run(context.Background(), "gh", "auth", "status")
	return err == nil && result.ExitCode == 0
}

// ghLogin runs interactive gh auth login with broad scopes.
// Uses RunAttached so the user can interact with the browser auth flow.
func ghLogin(ctx context.Context, rc *RunContext) error {
	args := []string{"auth", "login",
		"--hostname", "github.com",
		"--git-protocol", "https",
		"--web",
	}
	for _, s := range ghAuthScopes {
		args = append(args, "--scopes", s)
	}
	return rc.Runner.RunAttached(ctx, "gh", args...)
}

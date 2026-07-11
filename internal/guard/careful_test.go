package guard

import "testing"

func TestCheckCommand(t *testing.T) {
	cases := []struct {
		name    string
		command string
		pattern string // "" means allow
	}{
		// rm recursive
		{"rm -rf", "rm -rf /tmp/x", "rm_recursive"},
		{"rm -r", "rm -r src", "rm_recursive"},
		{"rm --recursive", "rm --recursive src", "rm_recursive"},
		{"rm -fr combined flags", "rm -fr src", "rm_recursive"},
		{"plain rm", "rm file.txt", ""},
		{"rm in longer pipeline", "make clean && rm -rf ./output", "rm_recursive"},

		// rm allowlist: build artifacts pass
		{"rm -rf node_modules", "rm -rf node_modules", ""},
		{"rm -rf nested node_modules", "rm -rf packages/app/node_modules", ""},
		{"rm -rf multiple safe", "rm -rf node_modules dist .next", ""},
		{"rm -rf dot cache", "rm -rf .cache", ""},
		{"rm -rf build", "rm -rf build", ""},
		{"rm -rf coverage and turbo", "rm -rf coverage .turbo", ""},
		{"rm -rf pycache", "rm -rf __pycache__", ""},
		{"rm -rf mixed safe and unsafe", "rm -rf node_modules src", "rm_recursive"},
		{"rm -rf unsafe path", "rm -rf /etc", "rm_recursive"},
		{"rm -rf safe with trailing slash is unsafe", "rm -rf node_modules/", "rm_recursive"},

		// compound commands: safe trailing rm must not mask earlier danger
		{"kubectl delete then safe rm", "kubectl delete pod prod-db; rm -rf node_modules", "kubectl_delete"},
		{"unsafe rm then safe rm", "rm -rf /important && rm -rf dist", "rm_recursive"},
		{"force push then safe rm", "git push --force && rm -rf dist", "git_force_push"},
		{"two safe rms in compound", "rm -rf node_modules && rm -rf dist", ""},

		// SQL
		{"drop table", "psql -c 'DROP TABLE users'", "drop_table"},
		{"drop database lowercase", "mysql -e 'drop database prod'", "drop_table"},
		{"truncate", "psql -c 'TRUNCATE users'", "truncate"},
		{"truncate case-insensitive", "echo 'Truncate logs'", "truncate"},
		{"truncated word does not match", "cat truncated.log", ""},

		// git
		{"git push force long", "git push --force origin main", "git_force_push"},
		{"git push force short", "git push -f", "git_force_push"},
		{"git push force-with-lease still warns", "git push --force-with-lease", "git_force_push"},
		{"git push plain", "git push origin main", ""},
		{"git reset hard", "git reset --hard HEAD~1", "git_reset_hard"},
		{"git reset soft", "git reset --soft HEAD~1", ""},
		{"git checkout dot", "git checkout .", "git_discard"},
		{"git restore dot", "git restore .", "git_discard"},
		{"git checkout branch", "git checkout main", ""},

		// kubectl / docker
		{"kubectl delete", "kubectl delete pod web-0", "kubectl_delete"},
		{"kubectl get", "kubectl get pods", ""},
		{"docker rm -f", "docker rm -f web", "docker_destructive"},
		{"docker system prune", "docker system prune", "docker_destructive"},
		{"docker ps", "docker ps -a", ""},

		// edge cases
		{"empty command", "", ""},
		{"whitespace only", "   ", ""},
		{"harmless command", "ls -la", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := CheckCommand(tc.command)
			if d.Pattern != tc.pattern {
				t.Fatalf("CheckCommand(%q) pattern = %q, want %q", tc.command, d.Pattern, tc.pattern)
			}
			wantPerm := ""
			if tc.pattern != "" {
				wantPerm = "ask"
			}
			if d.Permission != wantPerm {
				t.Fatalf("CheckCommand(%q) permission = %q, want %q", tc.command, d.Permission, wantPerm)
			}
			if tc.pattern != "" && d.Reason == "" {
				t.Fatalf("CheckCommand(%q) missing reason", tc.command)
			}
		})
	}
}

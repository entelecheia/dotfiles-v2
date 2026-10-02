package container

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// One case per row of the verb table and per flag rewrite in issue #223.
func TestTranslate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // backend argv; "" with note set means a no-op
		note bool
	}{
		// same verb
		{"run", "run --rm alpine uname -a", "run --rm alpine uname -a", false},
		{"create", "create --name web nginx", "create --name web nginx", false},
		{"start", "start web", "start web", false},
		{"stop", "stop web", "stop web", false},
		{"kill", "kill -s KILL web", "kill -s KILL web", false},
		{"exec", "exec -it web sh", "exec -it web sh", false},
		{"logs", "logs -f web", "logs -f web", false},
		{"inspect", "inspect web", "inspect web", false},
		{"stats", "stats --no-stream", "stats --no-stream", false},
		{"export", "export -o web.tar web", "export -o web.tar web", false},
		{"build", "build -t t .", "build -t t .", false},
		{"prune", "prune", "container prune --force", false},
		{"prune keeps -f", "prune -f", "container prune -f", false},
		// renamed verbs
		{"copy", "copy web:/etc/hosts .", "cp web:/etc/hosts .", false},
		{"cp", "cp web:/etc/hosts .", "cp web:/etc/hosts .", false},
		{"list", "list -a -q", "ps -a -q", false},
		{"ls", "ls", "ps", false},
		{"delete", "delete web", "rm web", false},
		{"rm", "rm -f web", "rm -f web", false},
		// image
		{"image list", "image list", "image ls", false},
		{"image ls alias", "i ls", "image ls", false},
		{"image delete", "image delete alpine", "image rm alpine", false},
		{"image rm", "image rm alpine", "image rm alpine", false},
		{"image pull", "image pull alpine", "image pull alpine", false},
		{"image push", "image push r/a", "image push r/a", false},
		{"image tag", "image tag a b", "image tag a b", false},
		{"image save", "image save -o a.tar a", "image save -o a.tar a", false},
		{"image load", "image load -i a.tar", "image load -i a.tar", false},
		{"image inspect", "image inspect a", "image inspect a", false},
		{"image prune", "image prune -a", "image prune -a --force", false},
		// registry
		{"registry login", "registry login ghcr.io", "login ghcr.io", false},
		{"registry logout", "r logout ghcr.io", "logout ghcr.io", false},
		// network and volume
		{"network create", "network create n1", "network create n1", false},
		{"network delete", "n delete n1", "network rm n1", false},
		{"network list", "network list", "network ls", false},
		{"network inspect", "network inspect n1", "network inspect n1", false},
		{"network prune", "network prune", "network prune --force", false},
		{"volume create", "volume create v1", "volume create v1", false},
		{"volume rm", "v rm v1", "volume rm v1", false},
		{"volume ls", "volume ls", "volume ls", false},
		{"volume inspect", "volume inspect v1", "volume inspect v1", false},
		{"volume prune", "volume prune", "volume prune --force", false},
		// system
		{"system status", "system status", "info", false},
		{"system version", "s version", "version", false},
		{"system df", "system df", "system df", false},
		{"system start", "system start", "", true},
		{"system stop", "system stop", "", true},
		{"builder start", "builder start", "", true},
		{"builder stop", "builder stop", "", true},
		{"builder status", "builder status", "", true},
		{"builder delete", "builder delete", "", true},
		// flag rewrites
		{"-c", "run -c 4 alpine", "run --cpus 4 alpine", false},
		{"--cpus", "create --cpus 2 alpine", "create --cpus 2 alpine", false},
		{"--cpus=", "run --cpus=2 alpine", "run --cpus 2 alpine", false},
		{"-a", "run -a amd64 alpine", "run --platform linux/amd64 alpine", false},
		{"--arch with --os", "run --os linux --arch arm64 alpine", "run --platform linux/arm64 alpine", false},
		{"--platform wins", "run --arch amd64 --platform linux/arm64 alpine", "run --platform linux/arm64 alpine", false},
		{"process args untouched", "run alpine sh -c 'echo' -a -c", "run alpine sh -c 'echo' -a -c", false},
		{"value flags before image", "run -e A=-c -v /a:/b -c 2 alpine", "run -e A=-c -v /a:/b --cpus 2 alpine", false},
		{"list --format json", "list --format json", "ps --format json", false},
		{"list --format=json", "ls --format=json -a", "ps --format json -a", false},
		{"list --format table", "list --format table", "ps", false},
		{"image list --format json", "image list --format json", "image ls --format json", false},
		// unknown passes through
		{"unknown verb", "frobnicate --x", "frobnicate --x", false},
		{"unknown flag", "run --rosetta alpine", "run --rosetta alpine", false},
		{"--gpus before rewrites", "run --gpus all -c 4 --rm img", "run --gpus all --cpus 4 --rm img", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Translate(strings.Fields(tc.in))
			if err != nil {
				t.Fatalf("Translate(%q): %v", tc.in, err)
			}
			if tc.note != (got.Note != "") {
				t.Fatalf("Translate(%q) note = %q, want note %v", tc.in, got.Note, tc.note)
			}
			if tc.note {
				if got.Args != nil {
					t.Fatalf("no-op %q must run nothing, got %q", tc.in, got.Args)
				}
				return
			}
			if want := strings.Fields(tc.want); !reflect.DeepEqual(got.Args, want) {
				t.Fatalf("Translate(%q) = %q, want %q", tc.in, got.Args, want)
			}
		})
	}
}

func TestTranslateUnsupportedExitsTwo(t *testing.T) {
	for _, in := range []string{
		"system kernel set --recommended", "system dns create x", "system property list", "system logs",
		"registry list", "r ls", "machine list", "m ls", "k8s up", "clean web",
		"run -k /vmlinux alpine", "run --kernel=/vmlinux alpine", "create --kernel /vmlinux alpine",
		"list --format yaml", "image ls --format=yaml", "ls --format toml",
	} {
		_, err := Translate(strings.Fields(in))
		var exit *ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Errorf("Translate(%q) error = %v, want exit 2", in, err)
		}
		if err != nil && !strings.Contains(err.Error(), "not supported on the Linux backend") {
			t.Errorf("Translate(%q) error %q lacks the unsupported message", in, err)
		}
	}
}

func TestTranslateDanglingValueFlagPassesThrough(t *testing.T) {
	got, err := Translate([]string{"run", "--name"})
	if err != nil || !reflect.DeepEqual(got.Args, []string{"run", "--name"}) {
		t.Fatalf("got %q, %v; the backend reports a missing value", got.Args, err)
	}
}

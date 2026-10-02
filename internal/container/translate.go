// Package container implements the shared `container` command: Apple
// container syntax on every host, run by apple/container on macOS and
// translated to the docker CLI dialect (docker or podman) on Linux.
package container

import (
	"fmt"
	"strings"
)

// Backend names, as they appear in modules.container.backend and state.json.
const (
	BackendAuto   = "auto"
	BackendApple  = "apple"
	BackendDocker = "docker"
	BackendPodman = "podman"
)

// ExitError ends the process with Code. cmd/dot honors ExitCode().
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// ExitCode is the status the process exits with.
func (e *ExitError) ExitCode() int { return e.Code }

func unsupported(what string) error {
	return &ExitError{Code: 2, Msg: fmt.Sprintf("container %s: not supported on the Linux backend", what)}
}

// Translation is one Apple argv rewritten for a docker-dialect backend.
type Translation struct {
	Args []string // backend argv, without the program name
	Note string   // set when the verb is a no-op on Linux: print it, run nothing, exit 0
}

// runValueFlags are the Apple run/create options that take a value. The flag
// scan needs them to find the image: everything after it belongs to the
// container's process and is never rewritten.
//
// ponytail: known ceiling. See docs/CEILINGS.md (container translation table).
var runValueFlags = map[string]bool{
	"-e": true, "--env": true, "--env-file": true, "--gid": true,
	"-u": true, "--user": true, "--uid": true,
	"-w": true, "--workdir": true, "--cwd": true, "--ulimit": true,
	"-c": true, "--cpus": true, "-m": true, "--memory": true,
	"-a": true, "--arch": true, "--os": true, "--platform": true,
	"--cap-add": true, "--cap-drop": true, "--cidfile": true,
	"--dns": true, "--dns-domain": true, "--dns-option": true, "--dns-search": true,
	"--entrypoint": true, "--init-image": true, "-k": true, "--kernel": true, "--kernel-arg": true,
	"-l": true, "--label": true, "--masked-path": true, "--mount": true, "--name": true,
	"--network": true, "-p": true, "--publish": true, "--publish-socket": true,
	"--read-only-path": true, "--runtime": true, "--shm-size": true, "--tmpfs": true,
	"-v": true, "--volume": true, "--scheme": true, "--progress": true,
	"--max-concurrent-downloads": true,
}

// flagSpan returns the index of the first positional argument (the image for
// run/create), or len(args) when there is none.
func flagSpan(args []string) int {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" || a == "-" || !strings.HasPrefix(a, "-") {
			return i
		}
		if name, _, hasEq := strings.Cut(a, "="); runValueFlags[name] && !hasEq {
			i++
		}
	}
	return len(args)
}

// Translate rewrites an Apple `container` argv into the docker CLI dialect,
// which docker and podman both accept.
func Translate(args []string) (Translation, error) {
	if len(args) == 0 {
		return Translation{}, nil
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "run", "create":
		out, err := rewriteRunFlags(rest)
		if err != nil {
			return Translation{}, err
		}
		return with(verb, out), nil
	case "start", "stop", "kill", "exec", "logs", "inspect", "stats", "export", "build":
		return with(verb, rest), nil
	case "prune": // docker has no top-level prune; Apple's removes stopped containers
		return with("container", append([]string{"prune"}, rest...)), nil
	case "copy", "cp":
		return with("cp", rest), nil
	case "list", "ls":
		return listVerb([]string{"ps"}, rest)
	case "delete", "rm":
		return with("rm", rest), nil
	case "image", "i":
		return translateImage(rest)
	case "registry", "r":
		return translateRegistry(rest)
	case "network", "n":
		return translateObject("network", rest)
	case "volume", "v":
		return translateObject("volume", rest)
	case "system", "s":
		return translateSystem(rest)
	case "builder":
		if len(rest) > 0 {
			switch rest[0] {
			case "start", "stop", "status", "delete":
				return Translation{Note: fmt.Sprintf("container builder %s: no-op on the Linux backend (buildkit is built in)", rest[0])}, nil
			}
		}
		return with("builder", rest), nil
	case "machine", "m", "k8s", "clean":
		return Translation{}, unsupported(verb)
	}
	return Translation{Args: args}, nil
}

func with(verb string, rest []string) Translation {
	return Translation{Args: append([]string{verb}, rest...)}
}

func translateImage(args []string) (Translation, error) {
	if len(args) == 0 {
		return with("image", nil), nil
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list", "ls":
		return listVerb([]string{"image", "ls"}, rest)
	case "delete", "rm":
		return with("image", append([]string{"rm"}, rest...)), nil
	}
	return with("image", args), nil
}

func translateRegistry(args []string) (Translation, error) {
	if len(args) > 0 {
		switch args[0] {
		case "login", "logout":
			return with(args[0], args[1:]), nil
		}
	}
	what := "registry"
	if len(args) > 0 {
		what += " " + args[0]
	}
	return Translation{}, unsupported(what)
}

func translateObject(kind string, args []string) (Translation, error) {
	if len(args) == 0 {
		return with(kind, nil), nil
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list", "ls":
		return listVerb([]string{kind, "ls"}, rest)
	case "delete", "rm":
		return with(kind, append([]string{"rm"}, rest...)), nil
	}
	return with(kind, args), nil
}

func translateSystem(args []string) (Translation, error) {
	if len(args) == 0 {
		return Translation{}, unsupported("system")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "status":
		return with("info", rest), nil
	case "version":
		return with("version", rest), nil
	case "df":
		return with("system", append([]string{"df"}, rest...)), nil
	case "start", "stop":
		return Translation{Note: fmt.Sprintf("container system %s: no-op on the Linux backend (the daemon is managed by the OS)", sub)}, nil
	}
	return Translation{}, unsupported("system " + sub)
}

// listVerb maps Apple's list --format (json|table|yaml) onto docker's:
// json is the same, table is docker's default output, yaml has no equivalent.
func listVerb(prefix, args []string) (Translation, error) {
	out := append([]string(nil), prefix...)
	for i := 0; i < len(args); i++ {
		a := args[i]
		value, ok := "", false
		switch {
		case a == "--format" && i+1 < len(args):
			value, ok = args[i+1], true
			i++
		case strings.HasPrefix(a, "--format="):
			value, ok = strings.TrimPrefix(a, "--format="), true
		}
		if !ok {
			out = append(out, a)
			continue
		}
		switch value {
		case "table":
		case "yaml":
			return Translation{}, unsupported("--format yaml")
		default:
			out = append(out, "--format", value)
		}
	}
	return Translation{Args: out}, nil
}

// rewriteRunFlags rewrites the run/create options whose meaning differs in
// docker: -c is --cpus (docker: --cpu-shares), -a is --arch (docker:
// --attach). --os and --arch merge into --platform, which wins over both as
// it does in Apple's CLI. Rewriting stops at the image.
func rewriteRunFlags(args []string) ([]string, error) {
	span := flagSpan(args)
	var out []string
	var osName, arch string
	hasPlatform := false
	for i := 0; i < span; i++ {
		a := args[i]
		name, value, hasEq := strings.Cut(a, "=")
		takesValue := runValueFlags[name] && !hasEq
		if takesValue {
			if i+1 >= span { // dangling option: the backend reports it
				out = append(out, a)
				continue
			}
			i++
			value = args[i]
		}
		switch name {
		case "-c", "--cpus":
			out = append(out, "--cpus", value)
		case "-a", "--arch":
			arch = value
		case "--os":
			osName = value
		case "--platform":
			hasPlatform = true
			out = append(out, "--platform", value)
		case "-k", "--kernel":
			return nil, unsupported(name)
		default:
			out = append(out, a)
			if takesValue {
				out = append(out, value)
			}
		}
	}
	if !hasPlatform && (arch != "" || osName != "") {
		platform := osName
		if platform == "" {
			platform = "linux"
		}
		if arch != "" {
			platform += "/" + arch
		}
		out = append(out, "--platform", platform)
	}
	return append(out, args[span:]...), nil
}

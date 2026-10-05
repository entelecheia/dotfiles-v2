package watchdog

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotifier_NtfyPOST(t *testing.T) {
	var gotMethod, gotTitle, gotPriority, gotTags, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotTitle = r.Header.Get("Title")
		gotPriority = r.Header.Get("Priority")
		gotTags = r.Header.Get("Tags")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := &Notifier{
		Settings: NotifySettings{NtfyURL: srv.URL},
		GOOS:     "linux", // macOS channel inert off-darwin
	}
	if err := n.Notify(context.Background(), "critical", "reaped pid 100"); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotTitle != "dot watchdog" {
		t.Errorf("title = %q", gotTitle)
	}
	if gotPriority != "urgent" {
		t.Errorf("priority = %q, want urgent", gotPriority)
	}
	if gotTags != "rotating_light" {
		t.Errorf("tags = %q", gotTags)
	}
	if gotBody != "reaped pid 100" {
		t.Errorf("body = %q", gotBody)
	}
}

func TestNotifier_EmptyNtfyURLSkipsNetwork(t *testing.T) {
	n := &Notifier{Settings: NotifySettings{}, GOOS: "linux"}
	if err := n.Notify(context.Background(), "info", "hello"); err != nil {
		t.Fatalf("Notify with no channels configured must be a no-op: %v", err)
	}
}

func TestNotifier_NtfyFailureSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	n := &Notifier{Settings: NotifySettings{NtfyURL: srv.URL}, GOOS: "linux"}
	if err := n.Notify(context.Background(), "warn", "x"); err == nil {
		t.Fatal("a 5xx from ntfy must be an error, not a silent drop")
	}
}

func TestNotifier_ChannelFailureDoesNotBlockTelegram(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "tok")
	t.Setenv("TELEGRAM_CHAT_ID", "1")
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ntfy.Close()
	var telegramHits int
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		telegramHits++
	}))
	defer tg.Close()
	n := &Notifier{
		Settings: NotifySettings{
			NtfyURL:  ntfy.URL,
			Telegram: TelegramSettings{Enabled: true, EnvPath: filepath.Join(t.TempDir(), "missing.env"), APIBase: tg.URL},
		},
		GOOS: "linux",
	}
	err := n.Notify(context.Background(), "warn", "x")
	if err == nil || !strings.Contains(err.Error(), "ntfy") {
		t.Fatalf("the ntfy failure must still surface: %v", err)
	}
	if telegramHits != 1 {
		t.Fatalf("telegram must be attempted despite the earlier channel's failure, got %d hit(s)", telegramHits)
	}
}

func TestNotifier_MacOSSkippedOffDarwin(t *testing.T) {
	n := &Notifier{
		Settings: NotifySettings{MacOS: true},
		GOOS:     "linux",
		Runner:   nil, // would fail if osascript were attempted
	}
	if err := n.Notify(context.Background(), "info", "hello"); err != nil {
		t.Fatalf("macOS channel must not fire off-darwin: %v", err)
	}
}

func TestNotifier_MacOSOnDarwinNeedsARunner(t *testing.T) {
	n := &Notifier{
		Settings: NotifySettings{MacOS: true},
		GOOS:     "darwin",
		Runner:   nil,
	}
	if err := n.Notify(context.Background(), "info", "hello"); err == nil {
		t.Fatal("darwin delivery without a runner must error, not silently skip")
	}
}

func TestNtfyPriorityMap(t *testing.T) {
	for level, want := range map[string]string{
		"critical": "urgent",
		"warn":     "high",
		"info":     "default",
		"other":    "default",
	} {
		if got := ntfyPriority(level); got != want {
			t.Errorf("ntfyPriority(%q) = %q, want %q", level, got, want)
		}
	}
}

func TestAppleScriptEscape(t *testing.T) {
	if got := appleScriptEscape(`say "hi" \ now`); got != `say \"hi\" \\ now` {
		t.Errorf("appleScriptEscape = %q", got)
	}
}

package template

import (
	"strings"
	"testing"
)

func TestRender_StaticGitIgnore(t *testing.T) {
	e := NewEngine()

	out, err := e.Render("git/ignore", nil)
	if err != nil {
		t.Fatalf("Render git/ignore: %v", err)
	}

	content := string(out)
	// git/ignore is a static file; check known entries
	for _, entry := range []string{".DS_Store", ".env", "node_modules/"} {
		if !strings.Contains(content, entry) {
			t.Errorf("Render git/ignore: expected %q in output", entry)
		}
	}
}

func TestRender_InvalidTemplate(t *testing.T) {
	e := NewEngine()

	_, err := e.Render("nonexistent/template", nil)
	if err == nil {
		t.Error("Render with nonexistent template: expected error, got nil")
	}
}

func TestReadStatic(t *testing.T) {
	e := NewEngine()

	out, err := e.ReadStatic("git/ignore")
	if err != nil {
		t.Fatalf("ReadStatic git/ignore: %v", err)
	}

	if len(out) == 0 {
		t.Error("ReadStatic git/ignore: expected non-empty content")
	}

	if !strings.Contains(string(out), ".DS_Store") {
		t.Error("ReadStatic git/ignore: expected .DS_Store in content")
	}
}

func TestReadStatic_Missing(t *testing.T) {
	e := NewEngine()

	_, err := e.ReadStatic("nonexistent/file")
	if err == nil {
		t.Error("ReadStatic missing file: expected error, got nil")
	}
}

func TestRenderString(t *testing.T) {
	e := NewEngine()

	tmpl := "Hello, {{.Name}}! Email: {{.Email}}"
	data := map[string]any{
		"Name":  "Alice",
		"Email": "alice@example.com",
	}

	out, err := e.RenderString("test", tmpl, data)
	if err != nil {
		t.Fatalf("RenderString: %v", err)
	}

	got := string(out)
	if got != "Hello, Alice! Email: alice@example.com" {
		t.Errorf("RenderString: got %q, want %q", got, "Hello, Alice! Email: alice@example.com")
	}
}

func TestRenderString_WithFuncMap(t *testing.T) {
	e := NewEngine()

	// Test the custom func map functions registered in NewEngine
	cases := []struct {
		name    string
		tmpl    string
		data    map[string]any
		want    string
	}{
		{
			name: "toLower",
			tmpl: `{{toLower .V}}`,
			data: map[string]any{"V": "HELLO"},
			want: "hello",
		},
		{
			name: "toUpper",
			tmpl: `{{toUpper .V}}`,
			data: map[string]any{"V": "hello"},
			want: "HELLO",
		},
		{
			name: "trimSpace",
			tmpl: `{{trimSpace .V}}`,
			data: map[string]any{"V": "  hi  "},
			want: "hi",
		},
		{
			name: "replace",
			tmpl: `{{replace .V "foo" "bar"}}`,
			data: map[string]any{"V": "foo baz foo"},
			want: "bar baz bar",
		},
		{
			name: "join",
			tmpl: `{{join .V ", "}}`,
			data: map[string]any{"V": []string{"a", "b", "c"}},
			want: "a, b, c",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := e.RenderString(tc.name, tc.tmpl, tc.data)
			if err != nil {
				t.Fatalf("RenderString %s: %v", tc.name, err)
			}
			if string(out) != tc.want {
				t.Errorf("RenderString %s: got %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}

func TestRenderString_InvalidSyntax(t *testing.T) {
	e := NewEngine()

	_, err := e.RenderString("bad", "{{.Unclosed", nil)
	if err == nil {
		t.Error("RenderString with invalid syntax: expected error, got nil")
	}
}

func TestRender_WithTemplateData(t *testing.T) {
	e := NewEngine()

	// git/config.tmpl uses Name, Email, GithubUser etc.
	data := map[string]any{
		"Name":       "Test User",
		"Email":      "test@example.com",
		"GithubUser": "testuser",
		"GitSigning": false,
		"SSHKeyName": "id_ed25519",
	}

	out, err := e.Render("git/config.tmpl", data)
	if err != nil {
		t.Fatalf("Render git/config.tmpl: %v", err)
	}

	content := string(out)
	if !strings.Contains(content, "Test User") {
		t.Errorf("Render git/config.tmpl: expected Name in output, got:\n%s", content)
	}
	if !strings.Contains(content, "test@example.com") {
		t.Errorf("Render git/config.tmpl: expected Email in output, got:\n%s", content)
	}
}

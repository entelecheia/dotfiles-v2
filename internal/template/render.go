package template

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"strings"
	"text/template"
)

//go:embed all:templates
var embeddedTemplates embed.FS

// Engine wraps Go text/template rendering.
type Engine struct {
	templates embed.FS
	funcMap   template.FuncMap
}

// NewEngine creates a new template engine.
func NewEngine() *Engine {
	return &Engine{
		templates: embeddedTemplates,
		funcMap: template.FuncMap{
			"replace": strings.ReplaceAll,
			"quote": func(s string) string {
				return fmt.Sprintf("%q", s)
			},
			"trimSpace": strings.TrimSpace,
			"contains":  strings.Contains,
			"hasPrefix": strings.HasPrefix,
			"hasSuffix": strings.HasSuffix,
			"toLower":   strings.ToLower,
			"toUpper":   strings.ToUpper,
			"join":      strings.Join,
			"expandHome": func(s string) string {
				if strings.HasPrefix(s, "~/") {
					home, _ := os.UserHomeDir()
					return home + s[1:]
				}
				return s
			},
		},
	}
}

// Render renders a template by path relative to templates/ directory.
func (e *Engine) Render(templatePath string, data any) ([]byte, error) {
	content, err := e.templates.ReadFile("templates/" + templatePath)
	if err != nil {
		return nil, fmt.Errorf("reading template %q: %w", templatePath, err)
	}

	tmpl, err := template.New(templatePath).Funcs(e.funcMap).Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("parsing template %q: %w", templatePath, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing template %q: %w", templatePath, err)
	}

	return buf.Bytes(), nil
}

// RenderString renders a template from a string.
func (e *Engine) RenderString(name, content string, data any) ([]byte, error) {
	tmpl, err := template.New(name).Funcs(e.funcMap).Parse(content)
	if err != nil {
		return nil, fmt.Errorf("parsing template %q: %w", name, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing template %q: %w", name, err)
	}

	return buf.Bytes(), nil
}

// ReadStatic reads a static (non-template) file from the embedded templates.
func (e *Engine) ReadStatic(path string) ([]byte, error) {
	return e.templates.ReadFile("templates/" + path)
}

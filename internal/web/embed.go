package web

import (
	"embed"
	"html/template"
	"io"
	"os"
	"sync"
)

//go:embed tokens.css components.css shell.html
var files embed.FS

// ProductName is the provisional name shown as text. There is no wordmark.
const ProductName = "herdr-recall"

type shellData struct {
	Name string
	CSRF string
}

var (
	shellOnce sync.Once
	shellTpl  *template.Template
	shellErr  error
)

// Stylesheet returns one embedded CSS file. Any other name is missing.
func Stylesheet(name string) ([]byte, error) {
	switch name {
	case "tokens.css", "components.css":
		return files.ReadFile(name)
	default:
		return nil, os.ErrNotExist
	}
}

// RenderShell writes the authenticated page. It does not list machines.
func RenderShell(w io.Writer, csrf string) error {
	tpl, err := shellTemplate()
	if err != nil {
		return err
	}
	return tpl.Execute(w, shellData{Name: ProductName, CSRF: csrf})
}

func shellTemplate() (*template.Template, error) {
	shellOnce.Do(func() {
		raw, err := files.ReadFile("shell.html")
		if err != nil {
			shellErr = err
			return
		}
		shellTpl, shellErr = template.New("shell").Parse(string(raw))
	})
	return shellTpl, shellErr
}

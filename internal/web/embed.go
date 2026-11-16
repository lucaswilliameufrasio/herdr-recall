package web

import (
	"embed"
	"html/template"
	"io"
	"os"
	"sync"
)

//go:embed tokens.css components.css shell.html panel.html panel.js
var files embed.FS

// ProductName is the provisional name shown as text. There is no wordmark.
const ProductName = "herdr-recall"

type shellData struct {
	Name     string
	CSRF     string
	Page     string
	Machines []Machine
	Detail   Detail
	Code     string
	Expires  string
}

// Asset returns an embedded public asset and its content type.
func Asset(name string) ([]byte, string, error) {
	if name == "panel.js" {
		body, err := files.ReadFile(name)
		return body, "text/javascript; charset=utf-8", err
	}
	body, err := Stylesheet(name)
	return body, "text/css; charset=utf-8", err
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

// RenderMachinesPage renders the authenticated list.
func RenderMachinesPage(w io.Writer, csrf string, machines []Machine) error {
	tpl, err := shellTemplate()
	if err != nil {
		return err
	}
	return tpl.ExecuteTemplate(w, "shell.html", shellData{Name: ProductName, CSRF: csrf, Page: "machines", Machines: machines})
}

// RenderDetailPage renders a machine using data read by the server.
func RenderDetailPage(w io.Writer, csrf string, detail Detail) error {
	tpl, err := shellTemplate()
	if err != nil {
		return err
	}
	return tpl.ExecuteTemplate(w, "shell.html", shellData{Name: ProductName, CSRF: csrf, Page: "detail", Detail: detail})
}

// RenderErrorPage renders a generic error without exposing database details.
func RenderErrorPage(w io.Writer, csrf string) error {
	tpl, err := shellTemplate()
	if err != nil {
		return err
	}
	return tpl.ExecuteTemplate(w, "shell.html", shellData{Name: ProductName, CSRF: csrf, Page: "error"})
}

// RenderEnrollmentCode displays a newly generated code once, without a URL parameter.
func RenderEnrollmentCode(w io.Writer, csrf, code, expires string) error {
	tpl, err := shellTemplate()
	if err != nil {
		return err
	}
	return tpl.ExecuteTemplate(w, "shell.html", shellData{Name: ProductName, CSRF: csrf, Page: "code", Code: code, Expires: expires})
}

func shellTemplate() (*template.Template, error) {
	shellOnce.Do(func() {
		shellTpl, shellErr = template.New("shell.html").Funcs(template.FuncMap{
			"cell": func(value string) string {
				if value == "" {
					return "—"
				}
				return value
			},
		}).ParseFS(files, "shell.html", "panel.html")
	})
	return shellTpl, shellErr
}

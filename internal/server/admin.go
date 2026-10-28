package server

import (
	_ "embed"
	"html/template"
	"io"
)

//go:embed admin.html
var adminHTML string

var adminTmpl = template.Must(template.New("admin").Parse(adminHTML))

// writeAdmin serves the stopgap embedded admin. The Next.js admin in web/
// replaces it; this one exists so a dev store install shows real results.
func writeAdmin(w io.Writer, apiKey string) {
	adminTmpl.Execute(w, struct{ APIKey string }{apiKey})
}

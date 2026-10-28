package server

import (
	_ "embed"
	"html/template"
	"io"
)

//go:embed admin.html
var adminHTML string

var adminTmpl = template.Must(template.New("admin").Parse(adminHTML))

var exitTmpl = template.Must(template.New("exit").Parse(`<!doctype html>
<html><head><meta charset="utf-8">
<meta name="shopify-api-key" content="{{.APIKey}}">
<script src="https://cdn.shopify.com/shopifycloud/app-bridge.js"></script>
<title>Feed Doctor</title></head>
<body><p>Taking you to Shopify to approve Feed Doctor's permissions…</p>
<script>window.open({{.Target}}, "_top");</script>
</body></html>`))

// writeAdmin serves the stopgap embedded admin. The Next.js admin in web/
// replaces it; this one exists so a dev store install shows real results.
func writeAdmin(w io.Writer, apiKey string) {
	adminTmpl.Execute(w, struct{ APIKey string }{apiKey})
}

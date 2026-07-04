package main

import (
	"html/template"
	"net/http"

	"github.com/canavan-a/fleetman/internal/banner"
)

var homeTmpl = template.Must(template.New("home").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>fleetman</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
  html, body {
    margin: 0;
    background: #000;
    color: #ccc;
    font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
  }
  pre {
    margin: 0;
    padding: 1rem;
    white-space: pre;
  }
</style>
</head>
<body>
<pre>{{.Art}}</pre>
</body>
</html>
`))

type homePageData struct {
	Art string
}

// HandleHome serves a small landing page at GET /, public like /healthz —
// just the ASCII wordmark, no version/uptime or other identifying info
// (this is an unauthenticated public endpoint).
func HandleHome() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		homeTmpl.Execute(w, homePageData{Art: banner.Art})
	}
}

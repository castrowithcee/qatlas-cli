package web

import (
	_ "embed"
	"html/template"
	"net/http"
)

// stylesheetPath is the one route that serves the shared stylesheet every page of this interface links.
const stylesheetPath = "/static/style.css"

//go:embed style.css
var stylesheet []byte

// layoutPage names the page a shared layout renders: its title, and which navigation entry marks it as the
// current one ("" for a page that belongs to none).
type layoutPage struct {
	Title string
	Nav   string
}

// layoutFuncs is the template function set every template set that uses layoutTemplates must be parsed with.
var layoutFuncs = template.FuncMap{
	"page": func(title, nav string) layoutPage { return layoutPage{Title: title, Nav: nav} },
}

// layoutTemplates defines the shared page frame as two halves: "layout-top" opens the document up to the
// content area and "layout-bottom" closes it. A page renders {{template "layout-top" (page "Title" "nav")}},
// its own content, then {{template "layout-bottom"}}. It is meant to be parsed into every template set
// together with layoutFuncs; it ships no script, no inline style, and no external asset.
const layoutTemplates = `
{{define "layout-top"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · qatlas web</title>
<link rel="stylesheet" href="` + stylesheetPath + `">
</head>
<body>
<header class="site-header"><div class="site-header-inner">
<p class="site-title">qatlas</p>
<nav class="site-nav" aria-label="Main"><ul>
<li><a href="/"{{if eq .Nav "overview"}} aria-current="page"{{end}}>Overview</a></li>
<li><a href="/#credentials"{{if eq .Nav "credentials"}} aria-current="page"{{end}}>Credentials</a></li>
<li><a href="/#connections"{{if eq .Nav "connections"}} aria-current="page"{{end}}>Connections</a></li>
</ul></nav>
</div></header>
<main>
{{end}}
{{define "layout-bottom"}}</main>
</body>
</html>
{{end}}
`

// handleStylesheet serves the embedded stylesheet. It holds no secret and no per-run value, but it is
// reachable only by the coupled browser session like every page that links it.
func (s *Server) handleStylesheet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(stylesheet)
}

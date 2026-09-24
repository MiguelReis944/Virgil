package dashboard

import (
	"bytes"
	"html/template"
	"net/http"
	"strings"
)

type panelSection string

const (
	sectionOverview    panelSection = "overview"
	sectionExecutions  panelSection = "executions"
	sectionProtections panelSection = "protections"
	sectionProviders   panelSection = "providers"
	sectionUsage       panelSection = "usage"
	sectionHealth      panelSection = "health"
	sectionSettings    panelSection = "settings"
)

type navigationItem struct {
	Label   string
	Href    string
	Current bool
}

type pageData struct {
	Title         string
	ActiveSection panelSection
	Flash         string
	HasAuth       bool
	Navigation    []navigationItem
	Content       any
}

const panelCSP = "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"

var productNavigation = []struct {
	section panelSection
	label   string
	href    string
}{
	{sectionOverview, "Overview", "/dashboard"},
	{sectionExecutions, "Executions", "/dashboard/executions"},
	{sectionProtections, "Protections", "/dashboard/protections"},
	{sectionProviders, "Providers", "/dashboard/providers"},
	{sectionUsage, "Usage", "/dashboard/usage"},
	{sectionHealth, "Health", "/dashboard/health"},
	{sectionSettings, "Settings", "/dashboard/settings"},
}

const sharedCSS = `
:root {
  --bg: #04060B;
  --surface: #0B1422;
  --surface2: #0f1d30;
  --border: rgba(255,255,255,0.07);
  --border2: rgba(255,255,255,0.12);
  --primary: #6366f1;
  --primary-light: #818cf8;
  --accent-green: #34d399;
  --accent-red: #f87171;
  --accent-yellow: #fbbf24;
  --accent-blue: #60a5fa;
  --accent-purple: #a78bfa;
  --text: #e2e8f0;
  --text-muted: #64748b;
  --text-dim: #94a3b8;
}
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
body{font-family:'Inter',system-ui,sans-serif;background:var(--bg);color:var(--text);min-height:100vh;font-size:14px;line-height:1.5}
a{color:var(--primary-light);text-decoration:none}
a:hover{color:#c7d2fe}
header{
  display:flex;align-items:center;gap:1rem;
  padding:.9rem 2rem;
  background:rgba(11,20,34,0.8);
  border-bottom:1px solid var(--border);
  backdrop-filter:blur(8px);
  position:sticky;top:0;z-index:100;
}
.logo{font-size:.95rem;font-weight:700;letter-spacing:.08em;color:var(--text);display:flex;align-items:center;gap:.5rem}
.logo::before{content:"⬡";color:var(--primary);font-size:1.1rem}
.header-sep{color:var(--border2);font-size:1rem}
.header-label{font-size:.78rem;color:var(--text-muted)}
.header-right{margin-left:auto;display:flex;align-items:center;gap:.75rem}
.btn-sm{
  background:var(--surface2);border:1px solid var(--border2);border-radius:6px;
  color:var(--text-dim);font-size:.75rem;padding:.3rem .7rem;cursor:pointer;
  transition:border-color .15s,color .15s;
}
.btn-sm:hover{border-color:var(--primary);color:var(--text)}
main{padding:2rem;max-width:1200px;margin:0 auto}
.section-title{
  font-size:.7rem;font-weight:700;text-transform:uppercase;letter-spacing:.1em;
  color:var(--text-muted);margin-bottom:1rem;margin-top:2rem;
}
.section-title:first-child{margin-top:0}

/* cards */
.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:1rem;margin-bottom:2rem}
.card{
  background:var(--surface);border:1px solid var(--border);border-radius:12px;
  padding:1.25rem 1.5rem;position:relative;overflow:hidden;
}
.card::before{
  content:'';position:absolute;top:0;left:0;right:0;height:1px;
  background:linear-gradient(90deg,transparent,var(--primary-light),transparent);
  opacity:.4;
}
.card-label{font-size:.68rem;font-weight:600;text-transform:uppercase;letter-spacing:.1em;color:var(--text-muted);margin-bottom:.5rem}
.card-value{font-size:1.75rem;font-weight:700;line-height:1}
.card-sub{font-size:.72rem;color:var(--text-muted);margin-top:.35rem}
.val-green{color:var(--accent-green)}
.val-blue{color:var(--accent-blue)}
.val-red{color:var(--accent-red)}
.val-purple{color:var(--accent-purple)}
.val-yellow{color:var(--accent-yellow)}
.val-white{color:var(--text)}

/* table */
.table-wrap{background:var(--surface);border:1px solid var(--border);border-radius:12px;overflow:hidden;margin-bottom:1.5rem}
table{width:100%;border-collapse:collapse;font-size:.85rem}
thead th{
  text-align:left;padding:.65rem 1.1rem;
  font-size:.68rem;font-weight:700;text-transform:uppercase;letter-spacing:.1em;
  color:var(--text-muted);background:rgba(255,255,255,.02);
  border-bottom:1px solid var(--border);
}
thead th a{color:var(--text-muted)}
thead th a:hover{color:var(--text)}
thead th.sort-active a{color:var(--primary-light)}
tbody tr{border-bottom:1px solid var(--border);transition:background .1s}
tbody tr:last-child{border-bottom:none}
tbody tr:hover{background:rgba(255,255,255,.03)}
tbody td{padding:.7rem 1.1rem;color:var(--text-dim)}
td.mono{font-family:'SF Mono',monospace;font-size:.78rem;color:var(--text-muted)}
.no-data{text-align:center;padding:3.5rem;color:var(--text-muted);font-size:.88rem}

/* badges */
.badge{
  display:inline-flex;align-items:center;gap:.3rem;
  padding:.2rem .6rem;border-radius:20px;font-size:.7rem;font-weight:600;
}
.badge-success{background:rgba(52,211,153,.12);color:var(--accent-green);border:1px solid rgba(52,211,153,.2)}
.badge-err{background:rgba(248,113,113,.12);color:var(--accent-red);border:1px solid rgba(248,113,113,.2)}
.badge-openai{background:rgba(96,165,250,.1);color:var(--accent-blue);border:1px solid rgba(96,165,250,.2)}
.badge-anthropic{background:rgba(251,191,36,.1);color:var(--accent-yellow);border:1px solid rgba(251,191,36,.2)}
.badge-nvidia{background:rgba(52,211,153,.1);color:var(--accent-green);border:1px solid rgba(52,211,153,.2)}
.badge-other{background:rgba(167,139,250,.1);color:var(--accent-purple);border:1px solid rgba(167,139,250,.2)}

/* filter bar */
.filter-bar{display:flex;gap:.75rem;margin-bottom:1.25rem;align-items:center;flex-wrap:wrap}
.filter-bar label{font-size:.75rem;color:var(--text-muted)}
.filter-bar select,.filter-bar input{
  background:var(--surface2);border:1px solid var(--border2);border-radius:6px;
  color:var(--text);padding:.35rem .65rem;font-size:.8rem;outline:none;
}
.filter-bar select:focus,.filter-bar input:focus{border-color:var(--primary)}
.filter-bar button{
  background:var(--primary);border:none;border-radius:6px;
  color:#fff;padding:.35rem .9rem;font-size:.8rem;cursor:pointer;font-weight:500;
}
.filter-bar button:hover{background:var(--primary-light)}

/* chart */
.chart-wrap{background:var(--surface);border:1px solid var(--border);border-radius:12px;padding:1.25rem 1.5rem;margin-bottom:1.5rem}
.cost-chart{display:block;width:100%;max-width:700px}

/* run link */
a.run-link{color:var(--primary-light);font-family:'SF Mono',monospace;font-size:.78rem}

@keyframes pulse{0%,100%{opacity:1}50%{opacity:.3}}

/* show more */
.show-more-wrap{text-align:center;padding:.75rem;border-top:1px solid var(--border)}
.btn-show-more{
  background:none;border:1px solid var(--border2);border-radius:6px;
  color:var(--text-muted);font-size:.78rem;padding:.35rem 1.2rem;cursor:pointer;
}
.btn-show-more:hover{border-color:var(--primary);color:var(--text)}
.event-row-hidden,.run-row-hidden,.session-row-hidden{display:none}

footer{text-align:center;padding:2rem;font-size:.7rem;color:var(--text-muted);opacity:.6}

/* login */
.login-wrap{min-height:100vh;display:flex;align-items:center;justify-content:center}
.login-card{
  background:var(--surface);border:1px solid var(--border);border-radius:16px;
  padding:2.5rem 2rem;width:340px;position:relative;overflow:hidden;
}
.login-card::before{
  content:'';position:absolute;top:0;left:0;right:0;height:1px;
  background:linear-gradient(90deg,transparent,var(--primary-light),transparent);
}
.login-logo{font-size:1.3rem;font-weight:800;letter-spacing:.06em;margin-bottom:.5rem;display:flex;align-items:center;gap:.6rem}
.login-logo::before{content:"⬡";color:var(--primary);font-size:1.4rem}
.login-sub{font-size:.8rem;color:var(--text-muted);margin-bottom:2rem}
.form-label{display:block;font-size:.72rem;font-weight:600;text-transform:uppercase;letter-spacing:.08em;color:var(--text-muted);margin-bottom:.45rem}
.form-input{
  width:100%;padding:.65rem .85rem;
  background:var(--bg);border:1px solid var(--border2);border-radius:8px;
  color:var(--text);font-size:.9rem;outline:none;
}
.form-input:focus{border-color:var(--primary)}
.btn-primary{
  margin-top:1.25rem;width:100%;padding:.7rem;
  background:var(--primary);border:none;border-radius:8px;
  color:#fff;font-size:.9rem;font-weight:600;cursor:pointer;
}
.btn-primary:hover{background:var(--primary-light)}
.form-err{color:var(--accent-red);font-size:.78rem;margin-top:.75rem}
`

const panelLayout = `{{define "layout"}}<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Virgil — {{.Title}}</title><style>` + sharedCSS + `
.shell{display:grid;grid-template-columns:210px minmax(0,1fr);min-height:100vh}.sidebar{background:var(--surface);border-right:1px solid var(--border);padding:1.4rem 1rem}.brand{font-weight:800;letter-spacing:.08em;color:var(--text);margin:0 .6rem 1.5rem}.brand-sub{display:block;font-size:.68rem;font-weight:500;color:var(--text-muted);letter-spacing:.02em;margin-top:.2rem}.nav{display:grid;gap:.3rem}.nav a{color:var(--text-dim);padding:.55rem .7rem;border-radius:7px}.nav a:hover,.nav a[aria-current=page]{background:rgba(99,102,241,.16);color:var(--primary-light)}.page-head{display:flex;align-items:center;border-bottom:1px solid var(--border);padding:1rem 2rem}.page-head h1{font-size:1rem}.page-actions{margin-left:auto}.flash{padding:.7rem 1rem;background:rgba(96,165,250,.12);border:1px solid rgba(96,165,250,.2);border-radius:8px;margin-bottom:1rem}.risk-card{border-color:rgba(248,113,113,.25)}.safe-card{border-color:rgba(52,211,153,.2)}
@media(max-width:760px){.shell{grid-template-columns:1fr}.sidebar{border-right:0;border-bottom:1px solid var(--border)}.nav{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style></head><body><div class="shell"><aside class="sidebar"><div class="brand">VIRGIL<span class="brand-sub">Local AI protection</span></div><nav class="nav" aria-label="Primary">{{range .Navigation}}<a href="{{.Href}}"{{if .Current}} aria-current="page"{{end}}>{{.Label}}</a>{{end}}</nav></aside><section><div class="page-head"><h1>{{.Title}}</h1>{{if .HasAuth}}<form class="page-actions" method="post" action="/logout"><button class="btn-sm">Sign out</button></form>{{end}}</div><main>{{if .Flash}}<div class="flash" role="status">{{.Flash}}</div>{{end}}{{template "content" .Content}}</main></section></div></body></html>{{end}}`

func renderPage(w http.ResponseWriter, page pageData, body string, content any) error {
	page.Content = content
	page.Navigation = make([]navigationItem, 0, len(productNavigation))
	for _, item := range productNavigation {
		page.Navigation = append(page.Navigation, navigationItem{Label: item.label, Href: item.href, Current: item.section == page.ActiveSection})
	}
	tmpl, err := template.New("panel").Funcs(template.FuncMap{"join": func(values []string) string { return strings.Join(values, ", ") }}).Parse(panelLayout + body)
	if err != nil {
		return err
	}
	var output bytes.Buffer
	if err := tmpl.ExecuteTemplate(&output, "layout", page); err != nil {
		return err
	}
	setPanelHeaders(w.Header())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = output.WriteTo(w)
	return err
}

const renderedContentBody = `{{define "content"}}{{.HTML}}{{end}}`

func renderTemplatePage(w http.ResponseWriter, page pageData, tmpl *template.Template, content any) error {
	var body bytes.Buffer
	if err := tmpl.ExecuteTemplate(&body, "content", content); err != nil {
		return err
	}
	return renderPage(w, page, renderedContentBody, struct{ HTML template.HTML }{template.HTML(body.String())})
}

func setPanelHeaders(header http.Header) {
	header.Set("Content-Security-Policy", panelCSP)
	header.Set("X-Frame-Options", "DENY")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
}

func requirePanelAuth(password string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setPanelHeaders(w.Header())
		if !IsAuthed(r, password) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func PlaceholderHandler(password, title, activeSection string) http.Handler {
	return requirePanelAuth(password, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		const body = `{{define "content"}}<div class="card"><div class="card-label">{{.Title}}</div><p>This section is being prepared for the local Virgil panel.</p></div>{{end}}`
		if err := renderPage(w, pageData{Title: title, ActiveSection: panelSection(activeSection), HasAuth: password != ""}, body, struct{ Title string }{title}); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
		}
	}))
}

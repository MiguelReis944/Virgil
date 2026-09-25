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
:root{color-scheme:dark;--bg:#08090a;--sidebar:#0d0e10;--surface:#141518;--surface2:#1a1b1f;--surface-raised:#1a1b1f;--border:#292b30;--border2:#383a40;--border-subtle:#202226;--primary:#c6a15b;--primary-light:#dfbd76;--accent:#c6a15b;--accent-green:#48b88a;--accent-red:#e06c75;--accent-yellow:#d99a4e;--accent-blue:#79a8d8;--accent-purple:#a69acb;--text:#f2f0e9;--text-muted:#7e7f85;--text-dim:#b2b1ad}
*,*::before,*::after{box-sizing:border-box}
html{background:var(--bg)}
body{margin:0;font-family:Inter,Geist,"Segoe UI",system-ui,sans-serif;background:var(--bg);color:var(--text);min-height:100vh;font-size:14px;line-height:1.55;text-rendering:optimizeLegibility}
button,input,select,textarea{font:inherit}
a{color:var(--primary-light);text-decoration:none}
a:hover{color:var(--text)}
:focus-visible{outline:2px solid var(--accent);outline-offset:3px}
.skip-link{position:fixed;left:1rem;top:-4rem;z-index:1000;background:var(--text);color:var(--bg);padding:.6rem .9rem;border-radius:7px;font-weight:650}
.skip-link:focus{top:1rem}
.btn-sm,.btn-show-more{display:inline-flex;align-items:center;justify-content:center;min-height:34px;background:transparent;border:1px solid var(--border2);border-radius:7px;color:var(--text-dim);font-size:.78rem;padding:.35rem .75rem;cursor:pointer;transition:border-color .14s,color .14s,background .14s}
.btn-sm:hover,.btn-show-more:hover{border-color:#68665f;background:var(--surface-raised);color:var(--text)}
main{padding:2rem clamp(1.25rem,3vw,3rem) 4rem;max-width:1440px;margin:0 auto;width:100%}
.section-title{font-size:.82rem;font-weight:650;color:var(--text);margin:2.25rem 0 .85rem}
.section-title:first-child{margin-top:0}
.section-head{display:flex;justify-content:space-between;align-items:flex-end;gap:1rem;margin:0 0 1rem}.section-head h2{font-size:1rem;margin:0}.section-head p{color:var(--text-muted);margin:.25rem 0 0;font-size:.8rem}
.cards,.metric-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(210px,1fr));gap:12px;margin-bottom:1.5rem}
.card,.data-panel,.form-section,.code-panel{background:var(--surface);border:1px solid var(--border);border-radius:8px;padding:1.2rem 1.3rem;position:relative}
.card p{color:var(--text-dim);margin:.45rem 0}.card code{color:var(--text)}
.card-label{font-size:.74rem;font-weight:550;color:var(--text-dim);margin-bottom:.55rem}
.card-value{font-family:ui-monospace,"SFMono-Regular","Cascadia Code",monospace;font-size:1.75rem;font-weight:620;line-height:1.05;letter-spacing:-.04em;font-variant-numeric:tabular-nums}
.card-sub{font-size:.74rem;color:var(--text-muted);margin-top:.4rem}
.risk-card{border-color:rgba(224,108,117,.38)}.safe-card{border-color:rgba(72,184,138,.3)}
.val-green{color:var(--accent-green)}.val-blue{color:var(--accent-blue)}.val-red{color:var(--accent-red)}.val-purple{color:var(--accent-purple)}.val-yellow{color:var(--accent-yellow)}.val-white{color:var(--text)}
.table-wrap{background:var(--surface);border:1px solid var(--border);border-radius:8px;overflow:auto;margin-bottom:1.5rem}
table{width:100%;border-collapse:collapse;font-size:.83rem;font-variant-numeric:tabular-nums}
thead th{position:sticky;top:0;z-index:2;text-align:left;padding:.72rem 1rem;font-size:.7rem;font-weight:600;color:var(--text-muted);background:var(--sidebar);border-bottom:1px solid var(--border);white-space:nowrap}
thead th a{color:var(--text-muted)}thead th a:hover{color:var(--text)}thead th.sort-active a{color:var(--primary-light)}
tbody tr{border-bottom:1px solid var(--border-subtle);transition:background .12s}tbody tr:last-child{border-bottom:0}tbody tr:hover{background:rgba(255,255,255,.025)}tbody td{padding:.72rem 1rem;color:var(--text-dim)}
td.mono,.mono,a.run-link{font-family:ui-monospace,"SFMono-Regular","Cascadia Code",monospace;font-size:.78rem;font-variant-numeric:tabular-nums}.no-data{text-align:center;padding:3.5rem;color:var(--text-muted);font-size:.86rem}
.badge{display:inline-flex;align-items:center;gap:.35rem;padding:.2rem .55rem;border-radius:999px;font-size:.7rem;font-weight:600;border:1px solid currentColor}.badge::before{content:"";width:5px;height:5px;border-radius:50%;background:currentColor}
.badge-success{background:rgba(72,184,138,.08);color:var(--accent-green)}.badge-err{background:rgba(224,108,117,.08);color:var(--accent-red)}.badge-openai{background:rgba(121,168,216,.08);color:var(--accent-blue)}.badge-anthropic{background:rgba(217,154,78,.08);color:var(--accent-yellow)}.badge-nvidia{background:rgba(72,184,138,.08);color:var(--accent-green)}.badge-other{background:rgba(166,154,203,.08);color:var(--accent-purple)}
.filter-bar{display:flex;gap:.6rem;margin-bottom:1rem;align-items:center;flex-wrap:wrap;color:var(--text-muted);font-size:.78rem}.filter-bar label{color:var(--text-dim)}
input,select,textarea,.form-input{background:var(--sidebar);border:1px solid var(--border2);border-radius:7px;color:var(--text);padding:.5rem .65rem;outline:0;min-height:36px}input:focus,select:focus,textarea:focus,.form-input:focus{border-color:var(--accent);box-shadow:0 0 0 3px rgba(198,161,91,.12)}
.filter-bar button,.btn-primary{display:inline-flex;align-items:center;justify-content:center;background:var(--accent);border:1px solid var(--accent);border-radius:7px;color:#11100d;padding:.48rem .9rem;min-height:36px;font-size:.8rem;cursor:pointer;font-weight:650}.filter-bar button:hover,.btn-primary:hover{background:var(--primary-light);border-color:var(--primary-light)}
.chart-wrap{background:var(--surface);border:1px solid var(--border);border-radius:8px;padding:1.25rem 1.4rem;margin-bottom:1.5rem}.cost-chart{display:block;width:100%;max-width:760px}
.show-more-wrap{text-align:center;padding:.8rem;border-top:1px solid var(--border-subtle)}.event-row-hidden,.run-row-hidden,.session-row-hidden{display:none}
pre{margin:.8rem 0 0;background:#0a0a0b;border:1px solid var(--border);border-radius:7px;padding:1rem;overflow:auto;white-space:pre-wrap;word-break:break-word}code{font-family:ui-monospace,"SFMono-Regular","Cascadia Code",monospace;font-size:.84em}
footer{text-align:center;padding:2rem;font-size:.72rem;color:var(--text-muted)}
.login-wrap{min-height:100vh;display:flex;align-items:center;justify-content:center;padding:1.5rem}.login-card{background:var(--surface);border:1px solid var(--border);border-radius:10px;padding:2.5rem 2rem;width:min(100%,360px)}.login-logo{font-size:1.25rem;font-weight:750;letter-spacing:-.02em;margin-bottom:.4rem}.login-logo::before{content:"";display:inline-block;width:9px;height:9px;background:var(--accent);border-radius:2px;margin-right:.65rem;transform:rotate(45deg)}.login-sub{font-size:.82rem;color:var(--text-muted);margin-bottom:2rem}.form-label{display:block;font-size:.76rem;font-weight:600;color:var(--text-dim);margin-bottom:.4rem}.form-input{width:100%}.btn-primary{margin-top:1.1rem;width:100%}.form-err{color:var(--accent-red);font-size:.78rem;margin:.75rem 0}
@media(prefers-reduced-motion:reduce){*,*::before,*::after{scroll-behavior:auto!important;transition:none!important;animation:none!important}}
`

const panelLayout = `{{define "layout"}}<!doctype html>
<html lang="en" data-theme="golden-bough"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Virgil — {{.Title}}</title><style>` + sharedCSS + `
.shell{display:grid;grid-template-columns:224px minmax(0,1fr);min-height:100vh}.sidebar{position:sticky;top:0;height:100vh;background:var(--sidebar);border-right:1px solid var(--border-subtle);padding:1.25rem 1rem;display:flex;flex-direction:column}.brand{display:grid;grid-template-columns:26px 1fr;column-gap:.7rem;align-items:center;color:var(--text);margin:.15rem .55rem 1.9rem;font-size:.95rem;font-weight:720;letter-spacing:.02em}.brand-mark{grid-row:1/3;width:24px;height:24px;border:1px solid #8f7545;border-radius:6px;display:grid;place-items:center;color:var(--accent);font-family:ui-monospace,monospace;font-size:.72rem}.brand-sub{display:block;font-size:.68rem;font-weight:450;color:var(--text-muted);letter-spacing:0;margin-top:-.05rem}.nav{display:grid;gap:.2rem}.nav a{display:flex;align-items:center;color:var(--text-dim);padding:.58rem .68rem;border:1px solid transparent;border-radius:7px;font-size:.84rem;transition:background .14s,color .14s,border-color .14s}.nav a:hover{background:var(--surface);color:var(--text)}.nav a[aria-current=page]{background:var(--surface-raised);border-color:var(--border);color:var(--text)}.nav a[aria-current=page]::before{content:"";width:5px;height:5px;border-radius:50%;background:var(--accent);margin-right:.55rem;box-shadow:0 0 0 3px rgba(198,161,91,.1)}.nav-status{margin-top:auto;border-top:1px solid var(--border-subtle);padding:1rem .55rem 0;color:var(--text-muted);font-size:.7rem}.nav-status strong{display:flex;align-items:center;gap:.45rem;color:var(--text-dim);font-weight:550;margin-bottom:.2rem}.nav-status strong::before{content:"";width:6px;height:6px;border-radius:50%;background:var(--accent-green)}.content-shell{min-width:0}.page-head{position:sticky;top:0;z-index:20;display:flex;align-items:center;min-height:62px;border-bottom:1px solid var(--border-subtle);padding:.8rem clamp(1.25rem,3vw,3rem);background:rgba(8,9,10,.88);backdrop-filter:blur(14px)}.page-head h1{font-size:.98rem;font-weight:620;letter-spacing:-.01em;margin:0}.page-kicker{color:var(--text-muted);font-size:.7rem;margin-right:.7rem}.page-actions{margin-left:auto}.flash{padding:.75rem 1rem;background:rgba(198,161,91,.08);border:1px solid rgba(198,161,91,.28);border-radius:8px;margin-bottom:1rem;color:var(--text-dim)}
@media(max-width:760px){.shell{grid-template-columns:1fr}.sidebar{position:relative;height:auto;border-right:0;border-bottom:1px solid var(--border-subtle);padding:1rem}.brand{margin:0 .2rem 1rem}.nav{display:flex;overflow-x:auto;padding-bottom:.2rem}.nav a{white-space:nowrap}.nav-status{display:none}.page-head{top:0}.page-kicker{display:none}main{padding-top:1.25rem}.cards,.metric-grid{grid-template-columns:1fr}.table-wrap{margin-left:-.25rem;margin-right:-.25rem}}
</style></head><body><a class="skip-link" href="#main-content">Skip to content</a><div class="shell"><aside class="sidebar"><div class="brand"><span class="brand-mark" aria-hidden="true">V</span><span>Virgil</span><span class="brand-sub">Local AI protection</span></div><nav class="nav" aria-label="Primary">{{range .Navigation}}<a href="{{.Href}}"{{if .Current}} aria-current="page"{{end}}>{{.Label}}</a>{{end}}</nav><div class="nav-status"><strong>Protection local</strong>Deterministic guardrails</div></aside><section class="content-shell"><div class="page-head"><span class="page-kicker">Local control</span><h1>{{.Title}}</h1>{{if .HasAuth}}<form class="page-actions" method="post" action="/logout"><button class="btn-sm">Sign out</button></form>{{end}}</div><main id="main-content" tabindex="-1">{{if .Flash}}<div class="flash" role="status">{{.Flash}}</div>{{end}}{{template "content" .Content}}</main></section></div></body></html>{{end}}`

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

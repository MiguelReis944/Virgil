package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/storage"
)

// ContentStore is implemented by *storage.Journal — used by EventHandler.
type ContentStore interface {
	GetContent(ctx context.Context, eventID string) (storage.ContentRecord, error)
}

// ---- shared CSS / base style -----------------------------------------

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
.event-row-hidden,.run-row-hidden{display:none}

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

// ---- templates -------------------------------------------------------

const loginHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Virgil — Sign in</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800&display=swap" rel="stylesheet">
<style>` + sharedCSS + `</style>
</head>
<body>
<div class="login-wrap">
  <div class="login-card">
    <div class="login-logo">Virgil</div>
    <div class="login-sub">Local AI Proxy — Dashboard</div>
    <form method="POST" action="/login">
      <label class="form-label">Password</label>
      <input class="form-input" type="password" name="password" autofocus>
      <button class="btn-primary" type="submit">Sign in</button>
      {{if .Error}}<p class="form-err">{{.Error}}</p>{{end}}
    </form>
  </div>
</div>
</body>
</html>`

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Virgil — Dashboard</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
<style>` + sharedCSS + `</style>
<script>
// Auto-refresh every 15s; pause when the user is interacting with filters
(function(){
  var t = setTimeout(function(){ location.reload(); }, 15000);
  document.addEventListener('focusin', function(){ clearTimeout(t); });
})();
</script>
</head>
<body>
<header>
  <span class="logo">Virgil</span>
  <span class="header-sep">/</span>
  <span class="header-label">Dashboard</span>
  <div class="header-right">
    <a href="/setup" class="btn-sm">⚙ Setup</a>
    <span style="font-size:.72rem;color:var(--text-muted)">last {{.SinceHours}}h</span>
    <span id="refresh-dot" style="font-size:.65rem;color:var(--text-muted);display:flex;align-items:center;gap:.3rem">
      <span style="width:6px;height:6px;border-radius:50%;background:var(--accent-green);display:inline-block;animation:pulse 2s ease-in-out infinite"></span>live
    </span>
    {{if .HasAuth}}
    <form method="POST" action="/logout">
      <button type="submit" class="btn-sm">Sign out</button>
    </form>
    {{end}}
  </div>
</header>
<main>

  <!-- summary cards -->
  <div class="cards">
    <div class="card">
      <div class="card-label">Total Calls</div>
      <div class="card-value val-white">{{.TotalCalls}}</div>
      <div class="card-sub">{{.SuccessRate}}% success rate</div>
    </div>
    <div class="card">
      <div class="card-label">Total Cost</div>
      <div class="card-value val-blue">${{.TotalCost}}</div>
      <div class="card-sub">{{.TotalErrors}} errors</div>
    </div>
    <div class="card">
      <div class="card-label">Avg Latency</div>
      <div class="card-value val-purple">{{.AvgLatency}}ms</div>
      <div class="card-sub">per request</div>
    </div>
    <div class="card">
      <div class="card-label">Input Tokens</div>
      <div class="card-value val-green">{{.TotalInputTokens}}</div>
      <div class="card-sub">total in period</div>
    </div>
    <div class="card">
      <div class="card-label">Output Tokens</div>
      <div class="card-value val-yellow">{{.TotalOutputTokens}}</div>
      <div class="card-sub">total in period</div>
    </div>
  </div>

  <!-- filters -->
  <form class="filter-bar" method="get" action="/dashboard">
    <label>Model
      <select name="model">
        <option value="">All</option>
        {{range .AllModels}}<option{{if eq . $.FilterModel}} selected{{end}}>{{.}}</option>{{end}}
      </select>
    </label>
    <label>Provider
      <select name="provider">
        <option value="">All</option>
        {{range .AllProviders}}<option{{if eq . $.FilterProvider}} selected{{end}}>{{.}}</option>{{end}}
      </select>
    </label>
    <label>Since
      <input type="number" name="hours" value="{{.SinceHours}}" min="1" max="168" style="width:5rem"> h
    </label>
    <button type="submit">Apply</button>
  </form>

  <!-- hourly chart -->
  {{if .HourlySeries}}
  <p class="section-title">Cost per Hour</p>
  <div class="chart-wrap">
    <svg viewBox="0 0 700 130" xmlns="http://www.w3.org/2000/svg" class="cost-chart">
      <style>
        .bar{fill:rgba(99,102,241,.5);stroke:rgba(99,102,241,.8);stroke-width:.5}
        .bar:hover{fill:rgba(129,140,248,.8)}
        .axis-label{font:9px Inter,system-ui,sans-serif;fill:#64748b}
      </style>
      <line x1="0" y1="110" x2="700" y2="110" stroke="rgba(255,255,255,.06)" stroke-width="1"/>
      {{range .HourlySeries}}
      <rect class="bar" x="{{.BarX}}" y="{{barY .BarHeight}}" width="22" height="{{.BarHeight}}" rx="2"/>
      {{end}}
      {{range $i,$p := .HourlySeries}}{{if showLabel $i}}
      <text class="axis-label" x="{{$p.BarX}}" y="126" text-anchor="middle">{{$p.Label}}</text>
      {{end}}{{end}}
    </svg>
  </div>
  {{end}}

  <!-- breakdown table -->
  <p class="section-title">Breakdown by Provider / Model</p>
  {{if .Rows}}
  <div class="table-wrap">
    <table>
      <thead><tr>
        <th>Provider</th>
        <th class="{{if eq .SortBy "model"}}sort-active{{end}}">
          <a href="?hours={{.SinceHours}}&model={{.FilterModel}}&provider={{.FilterProvider}}&sort=model">Model ⇅</a>
        </th>
        <th class="{{if eq .SortBy "calls"}}sort-active{{end}}">
          <a href="?hours={{.SinceHours}}&model={{.FilterModel}}&provider={{.FilterProvider}}&sort=calls">Calls ⇅</a>
        </th>
        <th>Errors</th>
        <th>Err%</th>
        <th class="{{if eq .SortBy "cost"}}sort-active{{end}}">
          <a href="?hours={{.SinceHours}}&model={{.FilterModel}}&provider={{.FilterProvider}}&sort=cost">Cost (USD) ⇅</a>
        </th>
        <th>Avg Latency</th>
      </tr></thead>
      <tbody>{{range .Rows}}
      <tr>
        <td><span class="badge badge-{{.ProviderClass}}">{{.Provider}}</span></td>
        <td>{{.Model}}</td>
        <td>{{.Calls}}</td>
        <td style="color:{{if gt .Errors 0}}var(--accent-red){{else}}var(--text-muted){{end}}">{{.Errors}}</td>
        <td>{{.ErrorPct}}%</td>
        <td style="color:var(--accent-blue)">${{.CostStr}}</td>
        <td>{{.LatStr}}ms</td>
      </tr>{{end}}</tbody>
    </table>
  </div>
  {{else}}<div class="no-data">No data for this period.</div>{{end}}

  <!-- recent runs -->
  <p class="section-title">Recent Runs</p>
  {{if .Runs}}
  <div class="table-wrap">
    <table>
      <thead><tr>
        <th>Run ID</th><th>Provider</th><th>Model</th>
        <th>Calls</th><th>Cost (USD)</th><th>Avg Latency</th><th>Last Seen</th>
      </tr></thead>
      <tbody>{{range $i,$r := .Runs}}
      <tr{{if ge $i 10}} class="run-row-hidden"{{end}}>
        <td><a class="run-link" href="/dashboard/run/{{$r.RunID}}">{{$r.ShortID}}</a></td>
        <td><span class="badge badge-{{$r.ProviderClass}}">{{$r.Provider}}</span></td>
        <td>{{$r.Model}}</td>
        <td>{{$r.Calls}}</td>
        <td style="color:var(--accent-blue)">${{$r.CostStr}}</td>
        <td>{{$r.LatStr}}ms</td>
        <td class="mono">{{$r.LastSeenStr}}</td>
      </tr>{{end}}</tbody>
    </table>
    {{if gt (len .Runs) 10}}
    <div class="show-more-wrap">
      <button class="btn-show-more" onclick="showAllRuns(this)">Show all {{len .Runs}} runs ▾</button>
    </div>
    {{end}}
  </div>
  {{else}}<div class="no-data">No runs in this period.</div>{{end}}

<script>
function showAllRuns(btn){
  document.querySelectorAll('.run-row-hidden').forEach(r=>r.style.display='');
  btn.parentElement.style.display='none';
}
</script>

</main>
<footer>Virgil &middot; local AI proxy</footer>
</body>
</html>`

const runHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Virgil — Run {{.RunID}}</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
<style>` + sharedCSS + `</style>
</head>
<body>
<header>
  <span class="logo">Virgil</span>
  <span class="header-sep">/</span>
  <a href="/dashboard" style="font-size:.8rem;color:var(--text-muted)">← Dashboard</a>
  <span class="header-sep">/</span>
  <span class="header-label" style="font-family:'SF Mono',monospace;font-size:.78rem">{{.RunID}}</span>
</header>
<main>

  <!-- run summary cards -->
  <div class="cards">
    <div class="card">
      <div class="card-label">Events</div>
      <div class="card-value val-white">{{.EventCount}}</div>
      <div class="card-sub">in this run</div>
    </div>
    <div class="card">
      <div class="card-label">Total Cost</div>
      <div class="card-value val-blue">${{.TotalCostStr}}</div>
    </div>
    <div class="card">
      <div class="card-label">Errors</div>
      <div class="card-value {{if gt .ErrorCount 0}}val-red{{else}}val-green{{end}}">{{.ErrorCount}}</div>
    </div>
    <div class="card">
      <div class="card-label">Input Tokens</div>
      <div class="card-value val-green">{{.TotalInputTokens}}</div>
    </div>
    <div class="card">
      <div class="card-label">Output Tokens</div>
      <div class="card-value val-yellow">{{.TotalOutputTokens}}</div>
    </div>
  </div>

  <p class="section-title">Events</p>
  {{if .Events}}
  <div class="table-wrap">
    <table>
      <thead><tr>
        <th>Event ID</th><th>Provider</th><th>Model</th><th>Status</th>
        <th>In Tokens</th><th>Out Tokens</th><th>Cost (USD)</th><th>Latency</th><th>Time</th>
      </tr></thead>
      <tbody>{{range $i,$e := .Events}}
      <tr{{if ge $i 20}} class="event-row-hidden"{{end}}>
        <td class="mono"><a href="/dashboard/event/{{$e.EventID}}" style="color:var(--text-muted);font-size:.75rem">{{$e.ShortID}}</a></td>
        <td><span class="badge badge-{{$e.ProviderClass}}">{{$e.Provider}}</span></td>
        <td>{{$e.Model}}</td>
        <td><span class="badge {{if eq $e.Status "success"}}badge-success{{else}}badge-err{{end}}">{{$e.Status}}</span></td>
        <td style="color:var(--accent-green)">{{$e.InputTokens}}</td>
        <td style="color:var(--accent-yellow)">{{$e.OutputTokens}}</td>
        <td style="color:var(--accent-blue)">${{$e.CostStr}}</td>
        <td>{{$e.LatStr}}ms</td>
        <td class="mono">{{$e.TimeStr}}</td>
      </tr>{{end}}</tbody>
    </table>
    {{if gt (len .Events) 20}}
    <div class="show-more-wrap">
      <button class="btn-show-more" onclick="showAllEvents(this)">Show all {{len .Events}} events ▾</button>
    </div>
    {{end}}
  </div>
  {{else}}<div class="no-data">No events found for this run.</div>{{end}}
<script>
function showAllEvents(btn){
  document.querySelectorAll('.event-row-hidden').forEach(r=>r.style.display='');
  btn.parentElement.style.display='none';
}
</script>

</main>
<footer>Virgil &middot; local AI proxy</footer>
</body>
</html>`

// ---- template data types ---------------------------------------------

type dashData struct {
	HasAuth            bool
	SinceHours         int
	FilterModel        string
	FilterProvider     string
	SortBy             string
	TotalCalls         int64
	TotalErrors        int64
	TotalCost          string
	SuccessRate        string
	AvgLatency         string
	TotalInputTokens   int64
	TotalOutputTokens  int64
	AllModels          []string
	AllProviders       []string
	Rows               []usageRowDisplay
	Runs               []runRowDisplay
	HourlySeries       []HourPoint
}

type usageRowDisplay struct {
	Provider      string
	ProviderClass string
	Model         string
	Calls         int64
	Errors        int64
	ErrorPct      string
	CostStr       string
	LatStr        string
}

type runRowDisplay struct {
	RunID         string
	ShortID       string
	Provider      string
	ProviderClass string
	Model         string
	Calls         int64
	CostStr       string
	LatStr        string
	LastSeenStr   string
}

type runPageData struct {
	RunID             string
	EventCount        int
	ErrorCount        int64
	TotalCostStr      string
	TotalInputTokens  int64
	TotalOutputTokens int64
	Events            []eventDisplay
}

type eventDisplay struct {
	EventID       string
	ShortID       string
	Provider      string
	ProviderClass string
	Model         string
	Status        string
	InputTokens   int64
	OutputTokens  int64
	CostStr       string
	LatStr        string
	TimeStr       string
}

// ---- compiled templates ----------------------------------------------

const eventDetailHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Virgil — Event {{.EventID}}</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
<style>` + sharedCSS + `
.content-box{
  background:var(--surface);border:1px solid var(--border);border-radius:12px;
  padding:1.25rem 1.5rem;margin-bottom:1.5rem;
}
.content-box pre{
  white-space:pre-wrap;word-break:break-word;
  font-family:'SF Mono',monospace;font-size:.8rem;
  color:var(--text-dim);line-height:1.7;max-height:60vh;overflow-y:auto;
}
.msg-role{font-size:.65rem;font-weight:700;text-transform:uppercase;letter-spacing:.1em;margin-bottom:.4rem}
.msg-role.system{color:var(--accent-yellow)}
.msg-role.user{color:var(--accent-blue)}
.msg-role.assistant{color:var(--accent-green)}
.msg-block{border-bottom:1px solid var(--border);padding:.9rem 0}
.msg-block:last-child{border-bottom:none}
.no-content{color:var(--text-muted);font-size:.85rem;font-style:italic}
</style>
</head>
<body>
<header>
  <span class="logo">Virgil</span>
  <span class="header-sep">/</span>
  <a href="/dashboard" style="font-size:.8rem;color:var(--text-muted)">← Dashboard</a>
  <span class="header-sep">/</span>
  <span class="header-label" style="font-family:'SF Mono',monospace;font-size:.78rem">{{.EventID}}</span>
</header>
<main>

  <div class="cards" style="grid-template-columns:repeat(auto-fit,minmax(160px,1fr))">
    <div class="card">
      <div class="card-label">Status</div>
      <div class="card-value" style="font-size:1.1rem"><span class="badge {{if eq .Status "success"}}badge-success{{else}}badge-err{{end}}">{{.Status}}</span></div>
    </div>
    <div class="card">
      <div class="card-label">Provider</div>
      <div class="card-value" style="font-size:1.1rem"><span class="badge badge-{{.ProviderClass}}">{{.Provider}}</span></div>
    </div>
    <div class="card">
      <div class="card-label">Model</div>
      <div class="card-value val-white" style="font-size:1rem">{{.Model}}</div>
    </div>
    <div class="card">
      <div class="card-label">Latency</div>
      <div class="card-value val-purple">{{.LatStr}}ms</div>
    </div>
    <div class="card">
      <div class="card-label">Cost</div>
      <div class="card-value val-blue">${{.CostStr}}</div>
    </div>
    <div class="card">
      <div class="card-label">Tokens</div>
      <div class="card-value" style="font-size:1rem">
        <span style="color:var(--accent-green)">{{.InputTokens}}</span>
        <span style="color:var(--text-muted);font-size:.8rem"> in</span>
        /
        <span style="color:var(--accent-yellow)">{{.OutputTokens}}</span>
        <span style="color:var(--text-muted);font-size:.8rem"> out</span>
      </div>
    </div>
  </div>

  {{if .HasContent}}
  <p class="section-title">Prompt</p>
  <div class="content-box">
    {{if .Messages}}
    {{range .Messages}}
    <div class="msg-block">
      <div class="msg-role {{.Role}}">{{.Role}}</div>
      <pre>{{.Content}}</pre>
    </div>
    {{end}}
    {{else}}
    <p class="no-content">Prompt not available for this event.</p>
    {{end}}
  </div>

  <p class="section-title">Response</p>
  <div class="content-box">
    {{if .ResponseText}}
    <pre>{{.ResponseText}}</pre>
    {{else if .ErrorMessage}}
    <pre style="color:var(--accent-red)">{{.ErrorMessage}}</pre>
    {{else}}
    <p class="no-content">Response not available for this event.</p>
    {{end}}
  </div>
  {{else}}
  <div class="no-data" style="margin-top:2rem">
    Content capture is available for events after the last restart.<br>
    Older events were recorded before content capture was enabled.
  </div>
  {{end}}

</main>
<footer>Virgil &middot; local AI proxy</footer>
</body>
</html>`

var eventDetailTmpl = template.Must(template.New("event").Parse(eventDetailHTML))

var loginTmpl = template.Must(template.New("login").Parse(loginHTML))

var dashTmpl = template.Must(template.New("dash").Funcs(template.FuncMap{
	"barY":      func(h int) int { return 110 - h },
	"showLabel": func(i int) bool { return i%4 == 0 },
}).Parse(dashboardHTML))

var runTmpl = template.Must(template.New("run").Parse(runHTML))

// ---- handlers --------------------------------------------------------

// LoginHandler handles GET and POST /login.
func LoginHandler(password string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			input := r.FormValue("password")
			ok, err := Login(w, password, input)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if ok {
				http.Redirect(w, r, "/dashboard", http.StatusFound)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			_ = loginTmpl.Execute(w, map[string]string{"Error": "Invalid password."})
			return
		}
		_ = loginTmpl.Execute(w, map[string]string{})
	}
}

// LogoutHandler handles POST /logout.
func LogoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		Logout(w, r)
		http.Redirect(w, r, "/login", http.StatusFound)
	}
}

// DashboardHandler handles GET /dashboard.
func DashboardHandler(db *sql.DB, password string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !IsAuthed(r, password) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		q := r.URL.Query()
		hours := parseIntClamp(q.Get("hours"), 24, 1, 168)
		filterModel := q.Get("model")
		filterProvider := q.Get("provider")
		sortBy := q.Get("sort")

		since := time.Now().Add(-time.Duration(hours) * time.Hour)
		ctx := r.Context()

		summary, err := QuerySummary(ctx, db, since)
		if err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}
		usageRows, err := QueryUsage(ctx, db, since, filterModel, filterProvider)
		if err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}
		hourly, err := QueryHourlySeries(ctx, db, hours)
		if err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}
		runs, err := QueryRuns(ctx, db, since)
		if err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}
		allModels, err := QueryDistinctModels(ctx, db, since)
		if err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}
		allProviders, err := QueryDistinctProviders(ctx, db, since)
		if err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}
	

		displayRows := make([]usageRowDisplay, len(usageRows))
		for i, ur := range usageRows {
			errPct := "0.0"
			if ur.Calls > 0 {
				errPct = fmt.Sprintf("%.1f", float64(ur.Errors)/float64(ur.Calls)*100)
			}
			displayRows[i] = usageRowDisplay{
				Provider:      ur.Provider,
				ProviderClass: providerClass(ur.Provider),
				Model:         ur.Model,
				Calls:         ur.Calls,
				Errors:        ur.Errors,
				ErrorPct:      errPct,
				CostStr:       fmt.Sprintf("%.4f", ur.CostUSD),
				LatStr:        fmt.Sprintf("%.0f", ur.AvgLatMS),
			}
		}

		switch sortBy {
		case "model":
			sort.Slice(displayRows, func(i, j int) bool { return displayRows[i].Model < displayRows[j].Model })
		case "calls":
			sort.Slice(displayRows, func(i, j int) bool { return displayRows[i].Calls > displayRows[j].Calls })
		case "cost":
			sort.Slice(displayRows, func(i, j int) bool {
				a, _ := strconv.ParseFloat(displayRows[i].CostStr, 64)
				b, _ := strconv.ParseFloat(displayRows[j].CostStr, 64)
				return a > b
			})
		}

		runDisplay := make([]runRowDisplay, len(runs))
		for i, rr := range runs {
			shortID := rr.RunID
			if len(shortID) > 14 {
				shortID = shortID[:14] + "…"
			}
			runDisplay[i] = runRowDisplay{
				RunID:         rr.RunID,
				ShortID:       shortID,
				Provider:      rr.Provider,
				ProviderClass: providerClass(rr.Provider),
				Model:         rr.Model,
				Calls:         rr.Calls,
				CostStr:       fmt.Sprintf("%.4f", rr.CostUSD),
				LatStr:        fmt.Sprintf("%.0f", rr.AvgLatMS),
				LastSeenStr:   rr.LastSeen.Format("2006-01-02 15:04"),
			}
		}

		successRate := "100.0"
		if summary.TotalCalls > 0 {
			ok := summary.TotalCalls - summary.TotalErrors
			successRate = fmt.Sprintf("%.1f", float64(ok)/float64(summary.TotalCalls)*100)
		}

		data := dashData{
			HasAuth:           password != "",
			SinceHours:        hours,
			FilterModel:       filterModel,
			FilterProvider:    filterProvider,
			SortBy:            sortBy,
			TotalCalls:        summary.TotalCalls,
			TotalErrors:       summary.TotalErrors,
			TotalCost:         fmt.Sprintf("%.4f", summary.TotalCost),
			SuccessRate:       successRate,
			AvgLatency:        fmt.Sprintf("%.0f", summary.AvgLatMS),
			TotalInputTokens:  summary.TotalInputTokens,
			TotalOutputTokens: summary.TotalOutputTokens,
			AllModels:         allModels,
			AllProviders:      allProviders,
			Rows:              displayRows,
			Runs:              runDisplay,
			HourlySeries:      hourly,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := dashTmpl.Execute(w, data); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
		}
	}
}

// RunHandler handles GET /dashboard/run/{runID}.
func RunHandler(db *sql.DB, password string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !IsAuthed(r, password) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		runID := r.PathValue("runID")
		events, err := QueryRunEvents(r.Context(), db, runID)
		if err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}

		evDisplay := toEventDisplay(events)

		var totalCost float64
		var errCount, inTok, outTok int64
		for _, e := range events {
			totalCost += e.CostUSD
			if e.Status != "success" {
				errCount++
			}
			inTok += e.InputTokens
			outTok += e.OutputTokens
		}

		data := runPageData{
			RunID:             runID,
			EventCount:        len(events),
			ErrorCount:        errCount,
			TotalCostStr:      fmt.Sprintf("%.4f", totalCost),
			TotalInputTokens:  inTok,
			TotalOutputTokens: outTok,
			Events:            evDisplay,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := runTmpl.Execute(w, data); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
		}
	}
}

// EventHandler handles GET /dashboard/event/{eventID}.
func EventHandler(db *sql.DB, cs ContentStore, password string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !IsAuthed(r, password) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		eventID := r.PathValue("eventID")
		ctx := r.Context()

		type msgEntry struct {
			Role    string
			Content string
		}
		type eventDetailData struct {
			EventID       string
			Status        string
			ProviderClass string
			Provider      string
			Model         string
			LatStr        string
			CostStr       string
			InputTokens   int64
			OutputTokens  int64
			HasContent    bool
			Messages      []msgEntry
			ResponseText  string
			ErrorMessage  string
		}

		data := eventDetailData{EventID: eventID}

		if ev, err := QueryEventByID(ctx, db, eventID); err == nil {
			data.Status = ev.Status
			data.Provider = ev.Provider
			data.ProviderClass = providerClass(ev.Provider)
			data.Model = ev.Model
			data.LatStr = fmt.Sprintf("%d", ev.LatencyMS)
			data.CostStr = fmt.Sprintf("%.4f", ev.CostUSD)
			data.InputTokens = ev.InputTokens
			data.OutputTokens = ev.OutputTokens
		}

		if rec, err := cs.GetContent(ctx, eventID); err == nil {
			data.HasContent = true
			if rec.PromptJSON != "" {
				var msgs []struct {
					Role    string `json:"role"`
					Content any    `json:"content"`
				}
				if json.Unmarshal([]byte(rec.PromptJSON), &msgs) == nil {
					for _, m := range msgs {
						var content string
						switch v := m.Content.(type) {
						case string:
							content = v
						default:
							b, _ := json.MarshalIndent(v, "", "  ")
							content = string(b)
						}
						data.Messages = append(data.Messages, msgEntry{Role: m.Role, Content: content})
					}
				}
			}
			data.ResponseText = rec.ResponseText
			data.ErrorMessage = rec.ErrorMessage
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := eventDetailTmpl.Execute(w, data); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
		}
	}
}

// ---- helpers ---------------------------------------------------------

func toEventDisplay(events []EventRow) []eventDisplay {
	out := make([]eventDisplay, len(events))
	for i, e := range events {
		shortID := e.EventID
		if len(shortID) > 16 {
			shortID = shortID[:16] + "…"
		}
		out[i] = eventDisplay{
			EventID:       e.EventID,
			ShortID:       shortID,
			Provider:      e.Provider,
			ProviderClass: providerClass(e.Provider),
			Model:         e.Model,
			Status:        e.Status,
			InputTokens:   e.InputTokens,
			OutputTokens:  e.OutputTokens,
			CostStr:       fmt.Sprintf("%.4f", e.CostUSD),
			LatStr:        fmt.Sprintf("%d", e.LatencyMS),
			TimeStr:       e.CreatedAt.Format("2006-01-02 15:04:05"),
		}
	}
	return out
}

func providerClass(p string) string {
	switch strings.ToLower(p) {
	case "openai":
		return "openai"
	case "anthropic":
		return "anthropic"
	case "nvidia":
		return "nvidia"
	default:
		return "other"
	}
}

func parseIntClamp(s string, def, min, max int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

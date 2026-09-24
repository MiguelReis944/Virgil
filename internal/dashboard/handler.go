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

// ---- templates -------------------------------------------------------

const loginHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Virgil — Sign in</title>
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

const dashboardHTML = `{{define "content"}}

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
  <form class="filter-bar" method="get" action="/dashboard" id="filter-form">
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
    <label>Status
      <select name="status">
        <option value=""{{if eq .FilterStatus ""}} selected{{end}}>All</option>
        <option value="success"{{if eq .FilterStatus "success"}} selected{{end}}>Success only</option>
        <option value="error"{{if eq .FilterStatus "error"}} selected{{end}}>Errors only</option>
      </select>
    </label>
    <label>Lat ≥ <input type="number" name="min_lat" value="{{.FilterMinLat}}" min="0" style="width:4.5rem" placeholder="ms"> ms</label>
    <label>Lat ≤ <input type="number" name="max_lat" value="{{.FilterMaxLat}}" min="0" style="width:4.5rem" placeholder="ms"> ms</label>
    <div style="display:flex;gap:.4rem;align-items:center">
      <span style="font-size:.72rem;color:var(--text-muted)">Period:</span>
      <button type="button" class="quick-h{{if eq .SinceHours 1}} active{{end}}" onclick="setHours(1)">1h</button>
      <button type="button" class="quick-h{{if eq .SinceHours 6}} active{{end}}" onclick="setHours(6)">6h</button>
      <button type="button" class="quick-h{{if eq .SinceHours 24}} active{{end}}" onclick="setHours(24)">24h</button>
      <button type="button" class="quick-h{{if eq .SinceHours 168}} active{{end}}" onclick="setHours(168)">7d</button>
      <input type="hidden" name="hours" id="hours-input" value="{{.SinceHours}}">
    </div>
    <button type="submit">Apply</button>
    <a href="/dashboard" style="font-size:.75rem;color:var(--text-muted);padding:.3rem .5rem">Reset</a>
  </form>
  <style>
  .quick-h{background:var(--surface2);border:1px solid var(--border2);border-radius:5px;color:var(--text-muted);font-size:.75rem;padding:.25rem .6rem;cursor:pointer}
  .quick-h:hover,.quick-h.active{background:rgba(99,102,241,.2);border-color:var(--primary);color:var(--primary-light)}
  .gap-row td{text-align:center;padding:.3rem 1.1rem;font-size:.7rem;color:var(--text-muted);background:transparent;border-bottom:none;font-style:italic}
  .gap-row td::before{content:"";display:block;border-top:1px dashed var(--border);margin-bottom:.25rem}
  </style>
  <script>
  function setHours(h){
    document.getElementById('hours-input').value=h;
    document.getElementById('filter-form').submit();
  }
  </script>

  <!-- charts section with tab navigation -->
  {{if .HourlySeries}}
  <p class="section-title" style="margin-bottom:.7rem">Activity over Time</p>
  <style>
  /* ── chart tabs ──────────────────────────────────────────── */
  .ctab-bar{display:flex;gap:.3rem;margin-bottom:1rem;flex-wrap:wrap;border-bottom:1px solid var(--border);padding-bottom:.6rem}
  .ctab{background:transparent;border:none;border-radius:6px;color:var(--text-muted);
        font-size:.78rem;font-weight:500;padding:.35rem 1rem;cursor:pointer;transition:all .15s;letter-spacing:.01em}
  .ctab:hover{background:rgba(255,255,255,.05);color:var(--text)}
  .ctab.active{background:rgba(99,102,241,.18);color:var(--primary-light);font-weight:600}
  .ctab-pane{display:none}
  .ctab-pane.active{display:block}
  /* ── chart layout ────────────────────────────────────────── */
  .cg2{display:grid;grid-template-columns:repeat(2,1fr);gap:.9rem;margin-bottom:.5rem}
  @media(max-width:820px){.cg2{grid-template-columns:1fr}}
  .cg1{margin-bottom:.5rem}
  .ccard{background:var(--surface);border:1px solid var(--border);border-radius:12px;padding:1.1rem 1.4rem 1rem}
  .ccard-head{display:flex;justify-content:space-between;align-items:baseline;margin-bottom:.5rem}
  .ctitle{font-size:.68rem;font-weight:600;color:var(--text-muted);text-transform:uppercase;letter-spacing:.09em}
  .clegend{font-size:.7rem;font-weight:400;color:var(--text-muted);margin-left:.5rem}
  .cbig{font-size:1.25rem;font-weight:700;font-variant-numeric:tabular-nums;color:var(--text);letter-spacing:-.02em}
  .csvg{width:100%;display:block;overflow:visible}
  /* ── svg primitives ──────────────────────────────────────── */
  .yax{font:9.5px Inter,system-ui,sans-serif;fill:rgba(100,116,139,.65);dominant-baseline:middle}
  .xax{font:9px Inter,system-ui,sans-serif;fill:rgba(100,116,139,.7)}
  .vlab{font:8.5px Inter,system-ui,sans-serif;fill:rgba(255,255,255,.7);text-anchor:middle;
        dominant-baseline:middle;pointer-events:none;font-weight:500}
  .grid{stroke:rgba(255,255,255,.055);stroke-width:.8;stroke-dasharray:3 3}
  .axis-line{stroke:rgba(255,255,255,.1);stroke-width:1}
  /* ── zero-state ──────────────────────────────────────────── */
  .chart-zero{display:flex;align-items:center;justify-content:center;height:130px;
              color:var(--text-muted);font-size:.8rem;gap:.5rem;opacity:.6}
  /* ── distribution bars ───────────────────────────────────── */
  .dist-wrap{display:flex;flex-direction:column;gap:.7rem;margin-top:.2rem}
  .dist-row{display:flex;align-items:center;gap:.75rem;font-size:.8rem}
  .dist-label{min-width:9rem;max-width:12rem;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;
              color:var(--text-dim);flex-shrink:0}
  .dist-track{flex:1;background:rgba(255,255,255,.07);border-radius:6px;height:12px;overflow:hidden}
  .dist-fill{height:100%;border-radius:6px}
  .dist-pct{width:3rem;text-align:right;color:var(--text);font-weight:600;font-variant-numeric:tabular-nums;flex-shrink:0}
  </style>

  <div class="ctab-bar">
    <button class="ctab active" onclick="switchTab('volume',this)">Volume</button>
    <button class="ctab" onclick="switchTab('latency',this)">Latency</button>
    <button class="ctab" onclick="switchTab('tokens',this)">Tokens</button>
    <button class="ctab" onclick="switchTab('cost',this)">Cost</button>
    <button class="ctab" onclick="switchTab('dist',this)">Distribution</button>
  </div>

  <!-- TAB: Volume — Requests + Error Rate -->
  <div class="ctab-pane active" id="tp-volume">
    <div class="cg2">
      <div class="ccard">
        <div class="ccard-head">
          <span class="ctitle">Requests / Hour</span>
          <span class="cbig" style="color:rgba(129,140,248,.95)">{{fmtK .HMaxCalls}}<span style="font-size:.8rem;font-weight:400;color:var(--text-muted)"> peak</span></span>
        </div>
        <svg viewBox="0 0 560 118" class="csvg">
          <line class="axis-line" x1="48" y1="15" x2="48" y2="95"/>
          <line class="grid" x1="48" y1="15" x2="556" y2="15"/>
          <line class="grid" x1="48" y1="55" x2="556" y2="55"/>
          <line class="axis-line" x1="48" y1="95" x2="556" y2="95"/>
          <text class="yax" x="43" y="18" text-anchor="end">{{fmtK .HMaxCalls}}</text>
          <text class="yax" x="43" y="55" text-anchor="end">{{halfK .HMaxCalls}}</text>
          <text class="yax" x="43" y="95" text-anchor="end">0</text>
          {{range .HourlySeries}}
          <rect fill="rgba(99,102,241,.65)" rx="3"
                x="{{.BarX}}" y="{{callsBarY .CallsH}}" width="{{.BarW}}" height="{{.CallsH}}"/>
          {{if gt .CallsH 14}}
          <text class="vlab" x="{{.BarCX}}" y="{{valY .CallsH}}">{{fmtK .Calls}}</text>
          {{end}}
          {{end}}
          {{$n := len .HourlySeries}}
          {{range $i,$p := .HourlySeries}}{{if showLabel $i $n}}
          <text class="xax" x="{{$p.BarCX}}" y="110" text-anchor="middle">{{$p.Label}}</text>
          {{end}}{{end}}
        </svg>
      </div>
      <div class="ccard">
        <div class="ccard-head">
          <span class="ctitle">Error Rate / Hour</span>
          <span class="cbig" style="color:rgba(248,113,113,.95)">{{fmtPct .TotalCalls .TotalErrors}}<span style="font-size:.8rem;font-weight:400;color:var(--text-muted)"> avg</span></span>
        </div>
        <svg viewBox="0 0 560 118" class="csvg">
          <line class="axis-line" x1="48" y1="15" x2="48" y2="95"/>
          <line class="grid" x1="48" y1="15" x2="556" y2="15"/>
          <line class="grid" x1="48" y1="55" x2="556" y2="55"/>
          <line class="axis-line" x1="48" y1="95" x2="556" y2="95"/>
          <text class="yax" x="43" y="18" text-anchor="end">100%</text>
          <text class="yax" x="43" y="55" text-anchor="end">50%</text>
          <text class="yax" x="43" y="95" text-anchor="end">0%</text>
          {{range .HourlySeries}}
          <rect fill="rgba(239,68,68,.65)" rx="3"
                x="{{.BarX}}" y="{{errBarY .ErrRateH}}" width="{{.BarW}}" height="{{.ErrRateH}}"/>
          {{if gt .ErrRateH 14}}
          <text class="vlab" x="{{.BarCX}}" y="{{valY .ErrRateH}}">{{fmtPct .Calls .Errors}}</text>
          {{end}}
          {{end}}
          {{$n := len .HourlySeries}}
          {{range $i,$p := .HourlySeries}}{{if showLabel $i $n}}
          <text class="xax" x="{{$p.BarCX}}" y="110" text-anchor="middle">{{$p.Label}}</text>
          {{end}}{{end}}
        </svg>
      </div>
    </div>
  </div>

  <!-- TAB: Latency — line chart with gradient area -->
  <div class="ctab-pane" id="tp-latency">
    <div class="cg1">
      <div class="ccard">
        <div class="ccard-head">
          <span class="ctitle">Avg Latency / Hour</span>
          <span class="cbig" style="color:rgba(192,132,252,.95)">{{fmtMs .HMaxLatMS}}<span style="font-size:.8rem;font-weight:400;color:var(--text-muted)"> peak</span></span>
        </div>
        {{if isZeroF .HMaxLatMS}}
        <div class="chart-zero">No latency data in this period</div>
        {{else}}
        <svg viewBox="0 0 560 118" class="csvg">
          <defs>
            <linearGradient id="latGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stop-color="rgba(168,85,247,.4)"/>
              <stop offset="100%" stop-color="rgba(168,85,247,.02)"/>
            </linearGradient>
          </defs>
          <line class="axis-line" x1="48" y1="15" x2="48" y2="95"/>
          <line class="grid" x1="48" y1="15" x2="556" y2="15"/>
          <line class="grid" x1="48" y1="55" x2="556" y2="55"/>
          <line class="axis-line" x1="48" y1="95" x2="556" y2="95"/>
          <text class="yax" x="43" y="18" text-anchor="end">{{fmtMs .HMaxLatMS}}</text>
          <text class="yax" x="43" y="55" text-anchor="end">{{halfMs .HMaxLatMS}}</text>
          <text class="yax" x="43" y="95" text-anchor="end">0</text>
          {{if gt (len .HourlySeries) 1}}
          <polygon fill="url(#latGrad)" points="{{latLine .HourlySeries}} {{(index .HourlySeries (sub1 (len .HourlySeries))).BarCX}},95 {{(index .HourlySeries 0).BarCX}},95"/>
          {{end}}
          <polyline fill="none" stroke="rgba(168,85,247,.9)" stroke-width="2.2" stroke-linejoin="round" stroke-linecap="round"
                    points="{{latLine .HourlySeries}}"/>
          {{range .HourlySeries}}
          <circle cx="{{latCX .BarCX}}" cy="{{latCY .LatH}}" r="3.5" fill="rgba(192,132,252,1)" stroke="var(--surface)" stroke-width="2"/>
          {{end}}
          {{$n := len .HourlySeries}}
          {{range $i,$p := .HourlySeries}}{{if showLabel $i $n}}
          <text class="xax" x="{{$p.BarCX}}" y="110" text-anchor="middle">{{$p.Label}}</text>
          {{end}}{{end}}
        </svg>
        {{end}}
      </div>
    </div>
  </div>

  <!-- TAB: Tokens — dual bars (input / output) -->
  <div class="ctab-pane" id="tp-tokens">
    <div class="cg1">
      <div class="ccard">
        <div class="ccard-head">
          <span class="ctitle">Tokens / Hour
            <span class="clegend">
              <span style="color:rgba(52,211,153,.9)">■ Input</span>
              &ensp;<span style="color:rgba(251,146,60,.9)">■ Output</span>
            </span>
          </span>
          <span class="cbig" style="color:rgba(52,211,153,.9)">{{fmtK .HMaxInTok}}<span style="color:var(--text-muted);font-weight:400;font-size:.85rem"> / </span><span style="color:rgba(251,146,60,.9)">{{fmtK .HMaxOutTok}}</span><span style="font-size:.8rem;font-weight:400;color:var(--text-muted)"> peak</span></span>
        </div>
        {{if eq .HMaxInTok 0}}
        <div class="chart-zero">No token data in this period</div>
        {{else}}
        <svg viewBox="0 0 560 118" class="csvg">
          <line class="axis-line" x1="48" y1="15" x2="48" y2="95"/>
          <line class="grid" x1="48" y1="15" x2="556" y2="15"/>
          <line class="grid" x1="48" y1="55" x2="556" y2="55"/>
          <line class="axis-line" x1="48" y1="95" x2="556" y2="95"/>
          <text class="yax" x="43" y="18" text-anchor="end">{{fmtK .HMaxInTok}}</text>
          <text class="yax" x="43" y="55" text-anchor="end">{{halfK .HMaxInTok}}</text>
          <text class="yax" x="43" y="95" text-anchor="end">0</text>
          {{range .HourlySeries}}
          <rect fill="rgba(52,211,153,.65)" rx="2"
                x="{{.BarX}}" y="{{inTokBarY .InTokH}}" width="{{halfW .BarW}}" height="{{.InTokH}}"/>
          <rect fill="rgba(251,146,60,.65)" rx="2"
                x="{{tokOutX .BarCX .BarW}}" y="{{outTokBarY .OutTokH}}" width="{{halfW .BarW}}" height="{{.OutTokH}}"/>
          {{if gt .InTokH 16}}
          <text class="vlab" x="{{.BarCX}}" y="{{valY .InTokH}}">{{fmtK .InTokens}}</text>
          {{end}}
          {{end}}
          {{$n := len .HourlySeries}}
          {{range $i,$p := .HourlySeries}}{{if showLabel $i $n}}
          <text class="xax" x="{{$p.BarCX}}" y="110" text-anchor="middle">{{$p.Label}}</text>
          {{end}}{{end}}
        </svg>
        {{end}}
      </div>
    </div>
  </div>

  <!-- TAB: Cost -->
  <div class="ctab-pane" id="tp-cost">
    <div class="cg1">
      <div class="ccard">
        <div class="ccard-head">
          <span class="ctitle">Cost / Hour (USD)</span>
          <span class="cbig" style="color:rgba(96,165,250,.95)">{{fmtCost .HMaxCost}}<span style="font-size:.8rem;font-weight:400;color:var(--text-muted)"> peak</span></span>
        </div>
        {{if isZeroF .HMaxCost}}
        <div class="chart-zero">No cost data — configure pricing in virgil.toml to track spend</div>
        {{else}}
        <svg viewBox="0 0 560 118" class="csvg">
          <line class="axis-line" x1="48" y1="15" x2="48" y2="95"/>
          <line class="grid" x1="48" y1="15" x2="556" y2="15"/>
          <line class="grid" x1="48" y1="55" x2="556" y2="55"/>
          <line class="axis-line" x1="48" y1="95" x2="556" y2="95"/>
          <text class="yax" x="43" y="18" text-anchor="end">{{fmtCost .HMaxCost}}</text>
          <text class="yax" x="43" y="55" text-anchor="end">{{halfCost .HMaxCost}}</text>
          <text class="yax" x="43" y="95" text-anchor="end">$0</text>
          {{range .HourlySeries}}
          <rect fill="rgba(59,130,246,.65)" rx="3"
                x="{{.BarX}}" y="{{barY .BarHeight}}" width="{{.BarW}}" height="{{.BarHeight}}"/>
          {{if gt .BarHeight 14}}
          <text class="vlab" x="{{.BarCX}}" y="{{valY .BarHeight}}">{{fmtCost .CostUSD}}</text>
          {{end}}
          {{end}}
          {{$n := len .HourlySeries}}
          {{range $i,$p := .HourlySeries}}{{if showLabel $i $n}}
          <text class="xax" x="{{$p.BarCX}}" y="110" text-anchor="middle">{{$p.Label}}</text>
          {{end}}{{end}}
        </svg>
        {{end}}
      </div>
    </div>
  </div>

  <!-- TAB: Distribution -->
  <div class="ctab-pane" id="tp-dist">
    <div class="cg2">
      {{if .ModelBars}}
      <div class="ccard">
        <div class="ccard-head"><span class="ctitle">Model Distribution</span></div>
        <div class="dist-wrap">
          {{range .ModelBars}}
          <div class="dist-row">
            <div class="dist-label" title="{{.Label}}">{{.Label}}</div>
            <div class="dist-track">
              <div class="dist-fill" style="width:{{.Pct}}%;background:{{.Color}};opacity:.85"></div>
            </div>
            <div class="dist-pct">{{.Pct}}%</div>
          </div>
          {{end}}
        </div>
      </div>
      {{end}}
      {{if .ProviderBars}}
      <div class="ccard">
        <div class="ccard-head"><span class="ctitle">Provider Distribution</span></div>
        <div class="dist-wrap">
          {{range .ProviderBars}}
          <div class="dist-row">
            <div class="dist-label">{{.Label}}</div>
            <div class="dist-track">
              <div class="dist-fill" style="width:{{.Pct}}%;background:{{.Color}};opacity:.85"></div>
            </div>
            <div class="dist-pct">{{.Pct}}%</div>
          </div>
          {{end}}
        </div>
      </div>
      {{end}}
    </div>
  </div>

  <script>
  function switchTab(name, btn) {
    document.querySelectorAll('.ctab').forEach(function(b){ b.classList.remove('active'); });
    document.querySelectorAll('.ctab-pane').forEach(function(p){ p.classList.remove('active'); });
    btn.classList.add('active');
    document.getElementById('tp-'+name).classList.add('active');
  }
  </script>
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

  <!-- recent sessions -->
  <p class="section-title">Recent Sessions</p>
  {{if .Sessions}}
  <style>
  th.sortable{cursor:pointer;user-select:none;white-space:nowrap}
  th.sortable:hover{color:var(--text)}
  th.sort-asc::after{content:" ▲";font-size:.65rem;opacity:.8}
  th.sort-desc::after{content:" ▼";font-size:.65rem;opacity:.8}
  </style>
  <div class="table-wrap">
    <table id="sessions-table">
      <thead><tr>
        <th class="sortable" data-col="0" data-type="str">Time</th>
        <th class="sortable" data-col="1" data-type="str">Model</th>
        <th class="sortable" data-col="2" data-type="num">Calls</th>
        <th class="sortable" data-col="3" data-type="num">Errors</th>
        <th class="sortable" data-col="4" data-type="num">In Tokens</th>
        <th class="sortable" data-col="5" data-type="num">Out Tokens</th>
        <th class="sortable" data-col="6" data-type="str">Providers</th>
        <th class="sortable" data-col="7" data-type="num">Cost (USD)</th>
        <th class="sortable" data-col="8" data-type="num">Avg Latency</th>
      </tr></thead>
      <tbody id="sessions-tbody">{{range $i,$s := .Sessions}}
      <tr{{if ge $i 10}} class="session-row-hidden"{{end}} style="cursor:pointer"
          onclick="location.href='/dashboard/session/{{$s.BucketTS}}?model={{$s.Model}}'"
          data-row-idx="{{$i}}">
        <td class="mono" style="color:var(--text)">{{$s.TimeLabel}}</td>
        <td><span class="badge badge-openai" style="font-size:.7rem">{{$s.Model}}</span></td>
        <td>{{$s.Calls}}</td>
        <td style="color:{{if $s.HasErrors}}var(--accent-red){{else}}var(--text-muted){{end}}">{{$s.Errors}}</td>
        <td class="mono" style="color:var(--text-muted)">{{$s.InTokens}}</td>
        <td class="mono" style="color:var(--text-muted)">{{$s.OutTokens}}</td>
        <td style="color:var(--text-dim)">{{$s.Providers}}</td>
        <td style="color:var(--accent-blue)">${{$s.CostStr}}</td>
        <td>{{$s.LatStr}}ms</td>
      </tr>{{end}}</tbody>
    </table>
    {{if gt (len .Sessions) 10}}
    <div class="show-more-wrap">
      <button class="btn-show-more" id="sessions-more-btn">Show all {{len .Sessions}} sessions ▾</button>
    </div>
    {{end}}
  </div>
  {{else}}<div class="no-data">No sessions in this period.</div>{{end}}

<script>
// show-more
var sessionsMoreBtn = document.getElementById('sessions-more-btn');
if(sessionsMoreBtn){
  sessionsMoreBtn.addEventListener('click', function(){
    document.querySelectorAll('.session-row-hidden').forEach(function(r){ r.classList.remove('session-row-hidden'); });
    this.parentElement.style.display='none';
  });
}

// sortable columns
(function(){
  var tbl = document.getElementById('sessions-table');
  if(!tbl) return;
  var sortCol = -1, sortAsc = true;
  tbl.querySelectorAll('th.sortable').forEach(function(th){
    th.addEventListener('click', function(){
      var col = parseInt(th.dataset.col);
      var isNum = th.dataset.type === 'num';
      if(sortCol === col){ sortAsc = !sortAsc; }
      else { sortCol = col; sortAsc = true; }
      // update header indicators
      tbl.querySelectorAll('th.sortable').forEach(function(h){ h.classList.remove('sort-asc','sort-desc'); });
      th.classList.add(sortAsc ? 'sort-asc' : 'sort-desc');
      // sort rows
      var tbody = document.getElementById('sessions-tbody');
      var rows = Array.from(tbody.querySelectorAll('tr'));
      rows.sort(function(a, b){
        var av = (a.cells[col] && a.cells[col].textContent.trim()) || '';
        var bv = (b.cells[col] && b.cells[col].textContent.trim()) || '';
        if(isNum){
          var an = parseFloat(av.replace(/[^0-9.\-]/g,'')) || 0;
          var bn = parseFloat(bv.replace(/[^0-9.\-]/g,'')) || 0;
          return sortAsc ? an - bn : bn - an;
        }
        return sortAsc ? av.localeCompare(bv) : bv.localeCompare(av);
      });
      rows.forEach(function(r){ tbody.appendChild(r); });
    });
  });
})();
</script>

<script>
(function(){
  var params = new URLSearchParams(location.search);
  if(params.get('model') || params.get('provider') || params.get('status') || params.get('min_lat') || params.get('max_lat')) return;
  var timer = setTimeout(function(){ location.reload(); }, 15000);
  document.addEventListener('focusin', function(){ clearTimeout(timer); });
})();
</script>

{{end}}`

const runHTML = `{{define "content"}}

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
      <button class="btn-show-more" id="ev-more-btn">Show all {{len .Events}} events ▾</button>
    </div>
    {{end}}
  </div>
  {{else}}<div class="no-data">No events found for this run.</div>{{end}}
<script>
var evMoreBtn = document.getElementById('ev-more-btn');
if(evMoreBtn){
  evMoreBtn.addEventListener('click', function(){
    document.querySelectorAll('.event-row-hidden').forEach(function(r){ r.classList.remove('event-row-hidden'); });
    this.parentElement.style.display='none';
  });
}
</script>


{{end}}`

// ---- template data types ---------------------------------------------

type dashData struct {
	HasAuth            bool
	SinceHours         int
	FilterModel        string
	FilterProvider     string
	FilterStatus       string
	FilterMinLat       string
	FilterMaxLat       string
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
	Sessions           []sessionRowDisplay
	HourlySeries       []HourPoint
	ModelBars          []ModelBar
	ProviderBars       []ModelBar
	// max values for Y-axis labels
	HMaxCalls   int64
	HMaxLatMS   float64
	HMaxCost    float64
	HMaxInTok   int64
	HMaxOutTok  int64
}

type sessionRowDisplay struct {
	BucketTS   int64
	Model      string
	TimeLabel  string
	Calls      int64
	Errors     int64
	InTokens   int64
	OutTokens  int64
	CostStr    string
	LatStr     string
	Providers  string
	HasErrors  bool
	GapMins    int64 // minutes since the row above (newer); >0 means a gap
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

// ModelBar is one horizontal bar in the model-distribution chart.
type ModelBar struct {
	Label string
	Pct   int    // 0-100 share of total calls
	Color string // CSS var name
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

const eventDetailHTML = `{{define "content"}}<style>
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


{{end}}`

var eventDetailTmpl = template.Must(template.New("event").Parse(eventDetailHTML))

const sessionHTML = `{{define "content"}}

  <div class="cards" style="grid-template-columns:repeat(auto-fit,minmax(160px,1fr))">
    <div class="card">
      <div class="card-label">Events</div>
      <div class="card-value val-white">{{.EventCount}}</div>
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
      <button class="btn-show-more" id="ev-more-btn">Show all {{len .Events}} events ▾</button>
    </div>
    {{end}}
  </div>
  {{else}}<div class="no-data">No events found for this session.</div>{{end}}


<script>
var evMoreBtn = document.getElementById('ev-more-btn');
if(evMoreBtn){
  evMoreBtn.addEventListener('click', function(){
    document.querySelectorAll('.event-row-hidden').forEach(function(r){ r.classList.remove('event-row-hidden'); });
    this.parentElement.style.display='none';
  });
}
</script>{{end}}`

var sessionTmpl = template.Must(template.New("session").Parse(sessionHTML))

var loginTmpl = template.Must(template.New("login").Parse(loginHTML))

// chartTop is the top of the chart area (y=15, 80px tall, baseline at y=95)
const chartTop, chartBase = 15, 95

var dashTmpl = template.Must(template.New("dash").Funcs(template.FuncMap{
	// bar top-Y: base minus height
	"barY":      func(h int) int { return chartBase - h },
	"callsBarY": func(h int) int { return chartBase - h },
	"errBarY":   func(h int) int { return chartBase - h },
	"inTokBarY": func(h int) int { return chartBase - h },
	"outTokBarY":func(h int) int { return chartBase - h },
	// token out bar x = cx (in is left half, out right half)
	"tokOutX": func(cx, bw int) int { return cx },
	// half-bar width
	"halfW": func(bw int) int { return bw/2 - 1 },
	// line chart helpers
	"latCX":  func(cx int) int { return cx },
	"latCY":  func(h int) int { return chartBase - h },
	"showLabel": func(i, n int) bool {
		if n <= 8 { return true }
		step := n / 6
		if step < 1 { step = 1 }
		return i%step == 0
	},
	"latLine": func(pts []HourPoint) string {
		if len(pts) == 0 { return "" }
		var b strings.Builder
		for _, p := range pts {
			if b.Len() > 0 { b.WriteByte(' ') }
			fmt.Fprintf(&b, "%d,%d", p.BarCX, chartBase-p.LatH)
		}
		return b.String()
	},
	// value label Y (above bar, min 12px from top)
	"valY": func(h int) int {
		y := chartBase - h - 4
		if y < chartTop+8 { y = chartTop + 8 }
		return y
	},
	// number formatters
	"fmtK": func(v int64) string {
		if v >= 1_000_000 { return fmt.Sprintf("%.1fM", float64(v)/1_000_000) }
		if v >= 1_000     { return fmt.Sprintf("%.1fK", float64(v)/1_000) }
		return fmt.Sprintf("%d", v)
	},
	"fmtMs": func(v float64) string {
		if v >= 60_000 { return fmt.Sprintf("%.1fm", v/60_000) }
		if v >= 1_000  { return fmt.Sprintf("%.1fs", v/1_000) }
		return fmt.Sprintf("%.0fms", v)
	},
	"fmtCost": func(v float64) string {
		if v == 0 { return "$0" }
		if v < 0.01 { return fmt.Sprintf("$%.4f", v) }
		return fmt.Sprintf("$%.3f", v)
	},
	"fmtPct": func(calls, errs int64) string {
		if calls == 0 { return "0%" }
		return fmt.Sprintf("%.0f%%", float64(errs)/float64(calls)*100)
	},
	"half2":  func(v float64) float64 { return v / 2 },
	"halfK":  func(v int64) string {
		h := v / 2
		if h >= 1_000 { return fmt.Sprintf("%.1fK", float64(h)/1_000) }
		return fmt.Sprintf("%d", h)
	},
	// halfMs formats half of a millisecond value as a human-readable time string.
	"halfMs": func(v float64) string {
		h := v / 2
		if h >= 60_000 { return fmt.Sprintf("%.1fm", h/60_000) }
		if h >= 1_000  { return fmt.Sprintf("%.1fs", h/1_000) }
		return fmt.Sprintf("%.0fms", h)
	},
	"halfCost": func(v float64) string {
		h := v / 2
		if h == 0 { return "$0" }
		if h < 0.001 { return fmt.Sprintf("$%.5f", h) }
		return fmt.Sprintf("$%.4f", h)
	},
	"isZeroF": func(v float64) bool { return v == 0 },
	"sub1": func(n int) int { return n - 1 },
}).Parse(dashboardHTML))

var runTmpl = template.Must(template.New("run").Parse(runHTML))

// ---- handlers --------------------------------------------------------

// LoginHandler handles GET and POST /login.
func LoginHandler(password string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setPanelHeaders(w.Header())
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
		setPanelHeaders(w.Header())
		Logout(w, r)
		http.Redirect(w, r, "/login", http.StatusFound)
	}
}

// DashboardHandler handles GET /dashboard.
func DashboardHandler(db *sql.DB, password string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setPanelHeaders(w.Header())
		if !IsAuthed(r, password) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		q := r.URL.Query()
		hours := parseIntClamp(q.Get("hours"), 24, 1, 168)
		filterModel := q.Get("model")
		filterProvider := q.Get("provider")
		filterStatus := q.Get("status")
		filterMinLat := q.Get("min_lat")
		filterMaxLat := q.Get("max_lat")
		sortBy := q.Get("sort")
		minLat, _ := strconv.ParseInt(filterMinLat, 10, 64)
		maxLat, _ := strconv.ParseInt(filterMaxLat, 10, 64)

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
		sessions, err := QuerySessions(ctx, db, since, SessionFilter{
			Model:    filterModel,
			Provider: filterProvider,
			Status:   filterStatus,
			MinLatMS: minLat,
			MaxLatMS: maxLat,
		})
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

		sessionDisplay := make([]sessionRowDisplay, len(sessions))
		for i, s := range sessions {
			start := time.Unix(s.BucketTS, 0)
			label := start.Format("15:04")
			if start.Format("2006-01-02") != time.Now().Format("2006-01-02") {
				label = start.Format("Jan 2 15:04")
			}
			var gapMins int64
			if i > 0 {
				prevBucket := sessions[i-1].BucketTS
				// gap = difference between the previous (newer) row's bucket and this bucket, minus 1 min
				diff := (prevBucket - s.BucketTS) / 60
				if diff > 1 {
					gapMins = diff - 1
				}
			}
			sessionDisplay[i] = sessionRowDisplay{
				BucketTS:  s.BucketTS,
				Model:     s.Model,
				TimeLabel: label,
				Calls:     s.Calls,
				Errors:    s.Errors,
				InTokens:  s.InTokens,
				OutTokens: s.OutTokens,
				CostStr:   fmt.Sprintf("%.4f", s.CostUSD),
				LatStr:    fmt.Sprintf("%.0f", s.AvgLatMS),
				Providers: s.Providers,
				HasErrors: s.Errors > 0,
				GapMins:   gapMins,
			}
		}

		successRate := "100.0"
		if summary.TotalCalls > 0 {
			ok := summary.TotalCalls - summary.TotalErrors
			successRate = fmt.Sprintf("%.1f", float64(ok)/float64(summary.TotalCalls)*100)
		}

		// build model + provider distribution bars
		modelBars := buildDistBars(usageRows, "model")
		providerBars := buildDistBars(usageRows, "provider")

		// max values for Y-axis scale labels
		var hMaxCalls int64
		var hMaxLat, hMaxCost float64
		var hMaxInTok, hMaxOutTok int64
		for _, p := range hourly {
			if p.Calls > hMaxCalls       { hMaxCalls  = p.Calls     }
			if p.AvgLatMS > hMaxLat      { hMaxLat    = p.AvgLatMS  }
			if p.CostUSD > hMaxCost      { hMaxCost   = p.CostUSD   }
			if p.InTokens > hMaxInTok    { hMaxInTok  = p.InTokens  }
			if p.OutTokens > hMaxOutTok  { hMaxOutTok = p.OutTokens }
		}

		data := dashData{
			HasAuth:           password != "",
			SinceHours:        hours,
			FilterModel:       filterModel,
			FilterProvider:    filterProvider,
			FilterStatus:      filterStatus,
			FilterMinLat:      filterMinLat,
			FilterMaxLat:      filterMaxLat,
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
			Sessions:          sessionDisplay,
			HourlySeries:      hourly,
			ModelBars:         modelBars,
			ProviderBars:      providerBars,
			HMaxCalls:         hMaxCalls,
			HMaxLatMS:         hMaxLat,
			HMaxCost:          hMaxCost,
			HMaxInTok:         hMaxInTok,
			HMaxOutTok:        hMaxOutTok,
		}

		if err := renderTemplatePage(w, pageData{Title: "Usage", ActiveSection: sectionUsage, HasAuth: password != ""}, dashTmpl, data); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
		}
	}
}

// SessionHandler handles GET /dashboard/session/{bucketTS}.
func SessionHandler(db *sql.DB, password string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setPanelHeaders(w.Header())
		if !IsAuthed(r, password) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		bucketTS, err := strconv.ParseInt(r.PathValue("bucketTS"), 10, 64)
		if err != nil {
			http.Error(w, "invalid session", http.StatusBadRequest)
			return
		}
		model := r.URL.Query().Get("model")
		events, err := QuerySessionEvents(r.Context(), db, bucketTS, model)
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

		start := time.Unix(bucketTS, 0)
		timeLabel := start.Format("15:04") + " – " + start.Add(time.Minute).Format("15:04")
		if start.Format("2006-01-02") != time.Now().Format("2006-01-02") {
			timeLabel = start.Format("Jan 2, 15:04") + " – " + start.Add(time.Minute).Format("15:04")
		}
		if model != "" {
			timeLabel += " · " + model
		}

		type sessionPageData struct {
			TimeLabel         string
			EventCount        int
			ErrorCount        int64
			TotalCostStr      string
			TotalInputTokens  int64
			TotalOutputTokens int64
			Events            []eventDisplay
		}
		data := sessionPageData{
			TimeLabel:         timeLabel,
			EventCount:        len(events),
			ErrorCount:        errCount,
			TotalCostStr:      fmt.Sprintf("%.4f", totalCost),
			TotalInputTokens:  inTok,
			TotalOutputTokens: outTok,
			Events:            evDisplay,
		}
		if err := renderTemplatePage(w, pageData{Title: "Session " + timeLabel, ActiveSection: sectionUsage, HasAuth: password != ""}, sessionTmpl, data); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
		}
	}
}

// RunHandler handles GET /dashboard/run/{runID}.
func RunHandler(db *sql.DB, password string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setPanelHeaders(w.Header())
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
		if err := renderTemplatePage(w, pageData{Title: "Execution " + runID, ActiveSection: sectionExecutions, HasAuth: password != ""}, runTmpl, data); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
		}
	}
}

// EventHandler handles GET /dashboard/event/{eventID}.
func EventHandler(db *sql.DB, cs ContentStore, password string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setPanelHeaders(w.Header())
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

		if err := renderTemplatePage(w, pageData{Title: "Event " + eventID, ActiveSection: sectionUsage, HasAuth: password != ""}, eventDetailTmpl, data); err != nil {
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

var distBarColors = []string{
	"var(--primary)", "var(--accent-blue)", "var(--accent-green)",
	"var(--accent-yellow)", "var(--accent-red)", "var(--accent-purple)",
}

// buildDistBars aggregates calls by model or provider into horizontal-bar data.
// key is "model" or "provider".
func buildDistBars(rows []UsageRow, key string) []ModelBar {
	totals := map[string]int64{}
	for _, r := range rows {
		label := r.Model
		if key == "provider" {
			label = r.Provider
		}
		if label != "" {
			totals[label] += r.Calls
		}
	}
	var total int64
	for _, v := range totals { total += v }
	if total == 0 {
		return nil
	}
	// sort by calls desc
	type kv struct{ k string; v int64 }
	sorted := make([]kv, 0, len(totals))
	for k, v := range totals { sorted = append(sorted, kv{k, v}) }
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
	bars := make([]ModelBar, len(sorted))
	for i, item := range sorted {
		bars[i] = ModelBar{
			Label: item.k,
			Pct:   int(float64(item.v) / float64(total) * 100),
			Color: distBarColors[i%len(distBarColors)],
		}
	}
	return bars
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

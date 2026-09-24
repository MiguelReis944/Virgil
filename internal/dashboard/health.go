package dashboard

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/MiguelReis944/Virgil/internal/settings"
)

type HealthOptions struct {
	DB               *sql.DB
	StartedAt        time.Time
	Version          string
	AppliedProviders int
}

type healthPage struct {
	AppliedHash, PendingHash, RestartLabel, RestartClass string
	Uptime, SQLiteLabel, SQLiteClass, Version            string
	Providers, ActiveRuns, DeadLetters                   int64
}

const healthBody = `{{define "content"}}<div class="cards">
<div class="card"><div class="card-label">Core</div><div class="card-value val-green">Ready</div><div class="card-sub">Uptime {{.Uptime}}</div></div>
<div class="card"><div class="card-label">SQLite</div><div class="card-value {{.SQLiteClass}}">{{.SQLiteLabel}}</div><div class="card-sub">Local storage readiness</div></div>
<div class="card"><div class="card-label">Configured providers</div><div class="card-value val-blue">{{.Providers}}</div><div class="card-sub">Available from the local configuration</div></div>
<div class="card"><div class="card-label">Active runs</div><div class="card-value val-purple">{{.ActiveRuns}}</div><div class="card-sub">Starting or running now</div></div>
<div class="card"><div class="card-label">Dead letters</div><div class="card-value val-yellow">{{.DeadLetters}}</div><div class="card-sub">Deliveries that need attention</div></div>
<div class="card"><div class="card-label">Version</div><div class="card-value val-white">{{.Version}}</div><div class="card-sub">Running Virgil build</div></div>
</div><p class="section-title">Configuration</p><div class="cards">
<div class="card"><div class="card-label">Configuration status</div><div class="card-value {{.RestartClass}}">{{.RestartLabel}}</div><div class="card-sub">Restart Virgil after saving configuration changes.</div></div>
<div class="card"><div class="card-label">Applied config hash</div><code>{{.AppliedHash}}</code></div>
<div class="card"><div class="card-label">Pending config hash</div><code>{{.PendingHash}}</code></div>
</div>{{end}}`

func HealthHandler(store *settings.Store, appliedHash, password string, options ...HealthOptions) http.Handler {
	return requirePanelAuth(password, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			healthError(w, "health_store_unavailable", nil)
			return
		}
		pending, err := settings.HashFile(store.Path())
		if err != nil {
			healthError(w, "health_config_hash_failed", err)
			return
		}
		opt := HealthOptions{StartedAt: time.Now(), Version: "development"}
		if len(options) > 0 {
			opt = options[0]
		}
		if opt.StartedAt.IsZero() {
			opt.StartedAt = time.Now()
		}
		if opt.Version == "" {
			opt.Version = "development"
		}
		data := healthPage{
			AppliedHash: shortHash(appliedHash), PendingHash: shortHash(pending),
			RestartLabel: "Up to date", RestartClass: "val-green",
			Uptime: formatUptime(time.Since(opt.StartedAt)), SQLiteLabel: "Unavailable", SQLiteClass: "val-yellow",
			Providers: int64(opt.AppliedProviders), Version: opt.Version,
		}
		if appliedHash == "" || pending != appliedHash {
			data.RestartLabel, data.RestartClass = "Restart required", "val-yellow"
		}
		if opt.DB != nil {
			if err := opt.DB.PingContext(r.Context()); err != nil {
				healthError(w, "health_sqlite_ping_failed", err)
				return
			}
			data.SQLiteLabel, data.SQLiteClass = "Ready", "val-green"
			if err := opt.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM executions WHERE state IN ('starting','running')`).Scan(&data.ActiveRuns); err != nil {
				healthError(w, "health_active_runs_query_failed", err)
				return
			}
			if err := opt.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM outbox WHERE state = 'dead_letter'`).Scan(&data.DeadLetters); err != nil {
				healthError(w, "health_dead_letters_query_failed", err)
				return
			}
		}
		if err := renderPage(w, pageData{Title: "Health", ActiveSection: sectionHealth, HasAuth: password != ""}, healthBody, data); err != nil {
			healthError(w, "health_render_failed", err)
		}
	}))
}

func healthError(w http.ResponseWriter, code string, err error) {
	if err == nil {
		slog.Error("dashboard health unavailable", "code", code)
	} else {
		slog.Error("dashboard health unavailable", "code", code, "error", err)
	}
	http.Error(w, "Health information is temporarily unavailable.", http.StatusInternalServerError)
}

func formatUptime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Truncate(time.Minute)
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd %dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh %dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%dm", int(d/time.Minute))
}

func shortHash(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	if value == "" {
		return "unavailable"
	}
	return value
}

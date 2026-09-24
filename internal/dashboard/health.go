package dashboard

import (
	"log/slog"
	"net/http"

	"github.com/MiguelReis944/Virgil/internal/settings"
)

type healthPage struct{ AppliedHash, PendingHash, RestartLabel, RestartClass string }

const healthBody = `{{define "content"}}<div class="cards"><div class="card"><div class="card-label">Configuration status</div><div class="card-value {{.RestartClass}}">{{.RestartLabel}}</div><div class="card-sub">Restart Virgil after saving configuration changes.</div></div><div class="card"><div class="card-label">Applied config hash</div><code>{{.AppliedHash}}</code></div><div class="card"><div class="card-label">Pending config hash</div><code>{{.PendingHash}}</code></div></div>{{end}}`

func HealthHandler(store *settings.Store, appliedHash, password string) http.Handler {
	return requirePanelAuth(password, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pending, err := settings.HashFile(store.Path())
		if err != nil {
			slog.Error("hash pending configuration failed", "error", err)
			http.Error(w, "Could not read configuration status.", http.StatusInternalServerError)
			return
		}
		data := healthPage{AppliedHash: shortHash(appliedHash), PendingHash: shortHash(pending), RestartLabel: "Up to date", RestartClass: "val-green"}
		if appliedHash == "" || pending != appliedHash {
			data.RestartLabel = "Restart required"
			data.RestartClass = "val-yellow"
		}
		if err := renderPage(w, pageData{Title: "Health", ActiveSection: sectionHealth, HasAuth: password != ""}, healthBody, data); err != nil {
			slog.Error("render health failed", "error", err)
			http.Error(w, "render error", 500)
		}
	}))
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

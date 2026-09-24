package dashboard

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/settings"
)

type settingsPage struct {
	Retention, Prompts, Responses, ToolArguments, Password, Bind string
}

const settingsBody = `{{define "content"}}<div class="cards">
<div class="card"><div class="card-label">Retention</div><div class="card-value val-white">{{.Retention}}</div><div class="card-sub">Local event history</div></div>
<div class="card"><div class="card-label">Panel password</div><div class="card-value val-green">{{.Password}}</div><div class="card-sub">Authentication status only</div></div>
<div class="card"><div class="card-label">Server bind</div><div class="card-value val-blue">{{.Bind}}</div><div class="card-sub">Loopback only · Read only</div></div>
</div><p class="section-title">Privacy capture</p><div class="table-wrap"><table><thead><tr><th>Content</th><th>Status</th></tr></thead><tbody>
<tr><td>Prompts</td><td>{{.Prompts}}</td></tr><tr><td>Responses</td><td>{{.Responses}}</td></tr><tr><td>Tool arguments</td><td>{{.ToolArguments}}</td></tr>
</tbody></table></div><div class="card-sub">Change these values in the local configuration and restart Virgil to apply them.</div>{{end}}`

func SettingsHandler(store *settings.Store, password string, applied ...config.Config) http.Handler {
	return requirePanelAuth(password, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			settingsError(w, "settings_store_unavailable", nil)
			return
		}
		var cfg config.Config
		if len(applied) > 0 {
			cfg = applied[0]
		} else {
			var err error
			cfg, err = store.Load()
			if err != nil {
				settingsError(w, "settings_config_load_failed", err)
				return
			}
		}
		status := func(enabled bool) string {
			if enabled {
				return "Enabled"
			}
			return "Disabled"
		}
		passwordStatus := "Not configured"
		if password != "" {
			passwordStatus = "Configured"
		}
		data := settingsPage{
			Retention: retentionLabel(cfg.Storage.RetentionDays), Prompts: status(cfg.Privacy.CapturePrompts),
			Responses: status(cfg.Privacy.CaptureResponses), ToolArguments: status(cfg.Privacy.CaptureToolArguments),
			Password: passwordStatus, Bind: cfg.Server.Listen,
		}
		if err := renderPage(w, pageData{Title: "Settings", ActiveSection: sectionSettings, HasAuth: password != ""}, settingsBody, data); err != nil {
			settingsError(w, "settings_render_failed", err)
		}
	}))
}

func retentionLabel(days int) string {
	if days == 0 {
		return "Forever"
	}
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}

func settingsError(w http.ResponseWriter, code string, err error) {
	if err == nil {
		slog.Error("dashboard settings unavailable", "code", code)
	} else {
		slog.Error("dashboard settings unavailable", "code", code, "error", err)
	}
	http.Error(w, "Settings are temporarily unavailable.", http.StatusInternalServerError)
}

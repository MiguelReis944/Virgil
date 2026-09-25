package dashboard

import (
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/settings"
)

var protectionDecimal = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,9})?$`)
var protectionLabel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}$`)

type protectionsPage struct {
	config.GuardrailsConfig
	CSRF, Error string
}

const protectionsBody = `{{define "content"}}{{if .Error}}<div class="form-err">{{.Error}}</div>{{end}}<div class="card"><div class="card-label">Start with a small limit</div><p>For a first supervised test, set Calls per run to 3, save, and restart Virgil. A fourth request should be blocked and the execution should appear in <a href="/dashboard/executions">Executions</a>. Zero or a blank numeric limit disables that limit. Leave allowlists empty to allow every configured provider, model, or tool. Cost limits block requests when cost cannot be estimated.</p></div><form method="post" action="/dashboard/protections"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><div class="cards">
<label class="card">Calls per run<input name="max_requests_per_run" value="{{.MaxRequestsPerRun}}"><span class="card-sub">Maximum provider requests in one supervised execution.</span></label>
<label class="card">Input tokens per run<input name="max_input_tokens_per_run" value="{{.MaxInputTokensPerRun}}"></label><label class="card">Output tokens per run<input name="max_output_tokens_per_run" value="{{.MaxOutputTokensPerRun}}"></label><label class="card">Total tokens per run<input name="max_total_tokens_per_run" value="{{.MaxTotalTokensPerRun}}"></label>
<label class="card">Cost per run<input name="max_cost_per_run_usd" value="{{.MaxCostPerRunUSD}}"></label><label class="card">Duration (seconds)<input name="max_duration_seconds" value="{{.MaxDurationSeconds}}"></label><label class="card">Tool calls per run<input name="max_tool_calls_per_run" value="{{.MaxToolCallsPerRun}}"></label>
<label class="card">Allowed providers<input name="allowed_providers" value="{{join .AllowedProviders}}"></label><label class="card">Allowed models<input name="allowed_models" value="{{join .AllowedModels}}"></label><label class="card">Allowed tools<input name="allowed_tools" value="{{join .AllowedTools}}"></label>
<label class="card">Daily calls<input name="max_calls_per_day" value="{{.MaxCallsPerDay}}"></label><label class="card">Daily cost<input name="max_cost_per_day_usd" value="{{.MaxCostPerDayUSD}}"></label><div class="card"><div class="card-label">Repetition rule</div><p>Virgil trips after the fourth repeated tool call, tool error, or provider error (threshold: 3).</p></div></div><button class="btn-primary" type="submit">Save protections</button></form>{{end}}`

func ProtectionsHandler(store *settings.Store, password string) http.Handler {
	csrf := newCSRFGuard()
	return requirePanelAuth(password, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := protectionsPage{CSRF: csrf.token(r)}
		cfg, err := store.Load()
		if err != nil {
			slog.Error("load protection settings failed", "error", err)
			http.Error(w, "Could not load configuration.", 500)
			return
		}
		page.GuardrailsConfig = cfg.Guardrails
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad form", 400)
				return
			}
			if !csrf.valid(r, r.FormValue("csrf_token")) {
				http.Error(w, "invalid CSRF token", 403)
				return
			}
			parsed, err := parseGuardrails(r, page.GuardrailsConfig)
			if err != nil {
				page.Error = err.Error()
				setPanelHeaders(w.Header())
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusUnprocessableEntity)
			} else if err = store.Update(r.Context(), func(cfg *config.Config) error { cfg.Guardrails = parsed; return nil }); err != nil {
				slog.Error("save protection settings failed", "error", err)
				page.Error = "Could not save configuration. Check the Virgil logs."
				setPanelHeaders(w.Header())
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusUnprocessableEntity)
			} else {
				page.GuardrailsConfig = parsed
				if err := renderProtections(w, password, page, "Configuration saved. Restart Virgil to apply changes."); err != nil {
					http.Error(w, "render error", 500)
				}
				return
			}
		} else if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		if err := renderProtections(w, password, page, ""); err != nil {
			http.Error(w, "render error", 500)
		}
	}))
}
func renderProtections(w http.ResponseWriter, password string, page protectionsPage, flash string) error {
	return renderPage(w, pageData{Title: "Protections", ActiveSection: sectionProtections, HasAuth: password != "", Flash: flash}, protectionsBody, page)
}
func parseGuardrails(r *http.Request, g config.GuardrailsConfig) (config.GuardrailsConfig, error) {
	var err error
	parse := func(name string) (int64, error) {
		v := strings.TrimSpace(r.FormValue(name))
		if v == "" {
			return 0, nil
		}
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil || n < 0 {
			return 0, fmt.Errorf("%s cannot be negative and must be a whole number", name)
		}
		return n, nil
	}
	if g.MaxRequestsPerRun, err = parse("max_requests_per_run"); err != nil {
		return g, err
	}
	if g.MaxInputTokensPerRun, err = parse("max_input_tokens_per_run"); err != nil {
		return g, err
	}
	if g.MaxOutputTokensPerRun, err = parse("max_output_tokens_per_run"); err != nil {
		return g, err
	}
	if g.MaxTotalTokensPerRun, err = parse("max_total_tokens_per_run"); err != nil {
		return g, err
	}
	if g.MaxDurationSeconds, err = parse("max_duration_seconds"); err != nil {
		return g, err
	}
	if g.MaxToolCallsPerRun, err = parse("max_tool_calls_per_run"); err != nil {
		return g, err
	}
	if g.MaxCallsPerDay, err = parse("max_calls_per_day"); err != nil {
		return g, err
	}
	g.MaxCostPerRunUSD = strings.TrimSpace(r.FormValue("max_cost_per_run_usd"))
	g.MaxCostPerDayUSD = strings.TrimSpace(r.FormValue("max_cost_per_day_usd"))
	for name, value := range map[string]string{"cost per run": g.MaxCostPerRunUSD, "daily cost": g.MaxCostPerDayUSD} {
		if value != "" && !protectionDecimal.MatchString(value) {
			return g, fmt.Errorf("%s must be a non-negative decimal", name)
		}
	}
	g.AllowedProviders = csv(r.FormValue("allowed_providers"))
	g.AllowedModels = csv(r.FormValue("allowed_models"))
	g.AllowedTools = csv(r.FormValue("allowed_tools"))
	for name, values := range map[string][]string{"allowed providers": g.AllowedProviders, "allowed models": g.AllowedModels, "allowed tools": g.AllowedTools} {
		for _, value := range values {
			if !protectionLabel.MatchString(value) {
				return g, fmt.Errorf("%s contains an invalid value", name)
			}
		}
	}
	return g, nil
}
func csv(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

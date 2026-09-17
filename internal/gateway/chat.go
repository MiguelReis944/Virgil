package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/policies"
	"github.com/MiguelReis944/Virgil/internal/providers"
	"github.com/MiguelReis944/Virgil/internal/redaction"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

const maxChatRequest = 1 << 20

type route struct {
	adapter  providers.Adapter
	keyEnv   string
	provider string
}

type router struct {
	models         map[string]route
	getenv         func(string) string
	recorder       EventRecorder
	installationID string
	policy         *policies.Engine
	guardrails     config.GuardrailsConfig
}

func newRouter(cfg config.Config, client *http.Client, getenv func(string) string) (*router, error) {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	r := &router{models: make(map[string]route), getenv: getenv, guardrails: cfg.Guardrails}
	for name, provider := range cfg.Providers {
		if provider.Type != "openai-compatible" && provider.Type != "openai" {
			return nil, errors.New("unsupported provider type")
		}
		if provider.Model == "" || provider.BaseURL == "" {
			return nil, errors.New("provider model and base_url are required")
		}
		if _, exists := r.models[provider.Model]; exists {
			return nil, errors.New("duplicate configured model")
		}
		r.models[provider.Model] = route{
			adapter:  providers.NewOpenAICompatible(provider.BaseURL, client),
			keyEnv:   provider.APIKeyEnv,
			provider: name,
		}
	}
	return r, nil
}

func writeAPIError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"message": http.StatusText(status),
			"type":    "gateway_error",
			"code":    code,
		},
	})
}

func (router *router) chat(w http.ResponseWriter, req *http.Request) {
	req.Body = http.MaxBytesReader(w, req.Body, maxChatRequest)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		var sizeErr *http.MaxBytesError
		var netErr net.Error
		switch {
		case errors.As(err, &sizeErr):
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large")
		case errors.As(err, &netErr) && netErr.Timeout():
			writeAPIError(w, http.StatusRequestTimeout, "request_timeout")
		default:
			writeAPIError(w, http.StatusBadRequest, "invalid_request")
		}
		return
	}
	var selector struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
		Tools  []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &selector); err != nil || selector.Model == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	selected, ok := router.models[selector.Model]
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "model_not_configured")
		return
	}
	if err := selected.adapter.Validate(body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "unsupported_request")
		return
	}
	key, ok := router.resolveKey(req.Header.Get("Authorization"), selected.keyEnv)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "provider_key_required")
		return
	}
	upstreamReq, err := selected.adapter.Build(req.Context(), body, key)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_provider_config")
		return
	}
	trace, err := telemetry.NewTrace(req.Header.Get("traceparent"))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "trace_unavailable")
		return
	}
	runID, err := telemetry.ResolveRunID(req.Header.Get("X-Virgil-Run-ID"))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "run_id_unavailable")
		return
	}
	w.Header().Set("X-Virgil-Run-ID", runID)
	upstreamReq.Header.Set("traceparent", "00-"+trace.TraceID+"-"+trace.SpanID+"-01")
	started := time.Now()
	result := providers.Result{Status: "transport_error", UsageSource: "unknown", ErrorCode: "provider_transport_error"}
	var policyDecision *telemetry.PolicyDecision
	var reservationID string
	defer func() {
		if router.policy != nil && reservationID != "" && len(result.ToolCallFingerprints) > 0 {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), 2*time.Second)
			if err := router.policy.RecordToolCalls(ctx, runID, result.ToolCallFingerprints); err != nil {
				slog.Error("tool repetition update failed", "code", "tool_repetition_failed")
			}
			cancel()
		}
		if router.policy != nil && reservationID != "" && (result.Status == "provider_error" || result.Status == "transport_error" || result.Status == "success") {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), 2*time.Second)
			if err := router.policy.RecordProviderOutcome(ctx, runID, result.Status, result.ErrorCode); err != nil {
				slog.Error("provider repetition update failed", "code", "provider_repetition_failed")
			}
			cancel()
		}
		if router.policy != nil && reservationID != "" {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), 2*time.Second)
			if err := router.policy.Postflight(ctx, policies.OutcomeFacts{RunID: runID, ReservationID: reservationID, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens}); err != nil {
				slog.Error("policy reconciliation failed", "code", "policy_postflight_failed")
			}
			cancel()
		}
		if router.recorder == nil {
			return
		}
		event, err := telemetry.Build(telemetry.Attempt{
			InstallationID: router.installationID,
			RunID:          runID,
			TraceID:        trace.TraceID,
			SpanID:         trace.SpanID,
			Provider:       selected.provider,
			RequestedModel: selector.Model,
			ResponseModel:  result.ResponseModel,
			InputTokens:    result.InputTokens,
			OutputTokens:   result.OutputTokens,
			CachedTokens:   result.CachedTokens,
			UsageSource:    result.UsageSource,
			Status:         result.Status,
			ErrorCode:      result.ErrorCode,
			PolicyDecision: policyDecision,
			StartedAt:      started,
			EndedAt:        time.Now(),
		})
		if err != nil {
			slog.Error("event rejected", "code", "event_build_failed")
			return
		}
		event, err = redaction.Prepare(event)
		if err != nil {
			slog.Error("event rejected", "code", "event_redaction_failed")
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), 2*time.Second)
		defer cancel()
		if err := router.recorder.Record(ctx, event); err != nil {
			slog.Error("event record failed", "code", "journal_write_failed")
		}
	}()
	if router.policy != nil {
		toolNames := make([]string, 0, len(selector.Tools))
		for _, tool := range selector.Tools {
			toolNames = append(toolNames, tool.Function.Name)
		}
		var estimatedInput, estimatedOutput *int64
		if router.guardrails.EstimatedInputTokensPerCall > 0 {
			estimatedInput = &router.guardrails.EstimatedInputTokensPerCall
		}
		if router.guardrails.EstimatedOutputTokensPerCall > 0 {
			estimatedOutput = &router.guardrails.EstimatedOutputTokensPerCall
		}
		decision, err := router.policy.Preflight(req.Context(), policies.RequestFacts{
			RunID: runID, Provider: selected.provider, Model: selector.Model,
			ToolNames: toolNames, StartedAt: started,
			EstimatedCostUSD:     router.guardrails.EstimatedCostPerCallUSD,
			EstimatedInputTokens: estimatedInput, EstimatedOutputTokens: estimatedOutput,
		})
		if err != nil {
			result.ErrorCode = "policy_unavailable"
			writeAPIError(w, http.StatusServiceUnavailable, "policy_unavailable")
			return
		}
		if decision.Decision == "block" {
			policyDecision = &telemetry.PolicyDecision{Decision: decision.Decision, Reason: decision.Reason, Policy: decision.Policy, Attempt: decision.Attempt, Threshold: decision.Threshold}
			result.Status = "policy_block"
			result.ErrorCode = decision.Reason
			writePolicyBlock(w, decision)
			return
		}
		reservationID = decision.ReservationID
	}
	resp, err := selected.adapter.Do(upstreamReq)
	if err != nil {
		if req.Context().Err() != nil {
			result.Status = "client_cancelled"
			result.ErrorCode = "client_cancelled"
		}
		writeAPIError(w, http.StatusBadGateway, "provider_transport_error")
		return
	}
	if selector.Stream && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		streamResult, streamErr := selected.adapter.Stream(req.Context(), resp, w)
		result = streamResult
		if streamErr != nil {
			result.ErrorCode = "provider_stream_error"
			if result.Status == "client_cancelled" {
				result.ErrorCode = "client_cancelled"
			}
		}
		return
	}
	translated, err := selected.adapter.Translate(resp, w)
	result = translated
	if err != nil {
		result.Status = "provider_error"
		result.ErrorCode = "provider_response_error"
		result.UsageSource = "unknown"
		writeAPIError(w, http.StatusBadGateway, "provider_response_error")
	}
}

func writePolicyBlock(w http.ResponseWriter, decision policies.Decision) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": "Forbidden", "type": "policy_error", "code": decision.Reason,
			"decision": map[string]any{
				"decision": decision.Decision, "reason": decision.Reason, "policy": decision.Policy,
				"attempt": decision.Attempt, "threshold": decision.Threshold,
			},
		},
	})
}

func (router *router) resolveKey(header, providerKeyEnv string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	presented := parts[1]
	localToken := router.getenv("VIRGIL_LOCAL_APP_TOKEN")
	if localToken != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(localToken)) == 1 {
		if providerKeyEnv == "" {
			return "", false
		}
		providerKey := router.getenv(providerKeyEnv)
		return providerKey, providerKey != ""
	}
	return presented, true
}

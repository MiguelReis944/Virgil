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
	"github.com/MiguelReis944/Virgil/internal/pricing"
	"github.com/MiguelReis944/Virgil/internal/providers"
	"github.com/MiguelReis944/Virgil/internal/redaction"
	"github.com/MiguelReis944/Virgil/internal/storage"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

const maxChatRequest = 1 << 20

type route struct {
	adapter  providers.Adapter
	keyEnv   string
	provider string
	validate func(json.RawMessage) error
}

type router struct {
	models         map[string]route
	getenv         func(string) string
	recorder       EventRecorder
	installationID string
	policy         *policies.Engine
	guardrails     config.GuardrailsConfig
	privacy        config.PrivacyConfig
	pricingCfg     config.PricingConfig
	priceRegistry  *pricing.Registry
	executionAuth  ExecutionAuthenticator
}

// sharedTransport is optimised for many concurrent calls to the same upstream
// host (e.g. api.openai.com).  No Timeout is set on the Client so that SSE
// streams are not cut off; ResponseHeaderTimeout guards against slow headers.
var sharedTransport = &http.Transport{
	MaxIdleConns:          128,
	MaxIdleConnsPerHost:   64,
	IdleConnTimeout:       90 * time.Second,
	ResponseHeaderTimeout: 120 * time.Second,
}

func newRouter(cfg config.Config, client *http.Client, getenv func(string) string) (*router, error) {
	if client == nil {
		client = &http.Client{Transport: sharedTransport}
	}
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	// Build price registry from config for provider:model lookups.
	var priceSources []pricing.ModelPriceSource
	for model, entry := range cfg.Pricing.Models {
		priceSources = append(priceSources, pricing.ModelPriceSource{
			Model:          model,
			InputPerToken:  entry.InputPerToken,
			OutputPerToken: entry.OutputPerToken,
			CachedPerToken: entry.CachedPerToken,
		})
	}
	priceReg := pricing.NewPriceRegistryFromEntries(priceSources)
	r := &router{models: make(map[string]route), getenv: getenv, guardrails: cfg.Guardrails, privacy: cfg.Privacy, pricingCfg: cfg.Pricing, priceRegistry: priceReg}
	registry, err := providers.NewRegistry(cfg, client)
	if err != nil {
		return nil, err
	}
	for model, registration := range registry {
		r.models[model] = route{
			adapter:  registration.Adapter,
			keyEnv:   registration.KeyEnv,
			provider: registration.Provider,
			validate: registration.Validate,
		}
	}
	return r, nil
}

var errorMessages = map[string]string{
	"request_too_large":        "Request body exceeds the 1 MiB limit.",
	"request_timeout":          "The upstream read timed out.",
	"invalid_request":          "Request body is not valid JSON or is missing required fields.",
	"model_not_configured":     "The requested model is not configured in this gateway. Check the 'model' field or run 'virgil init' to generate a config.",
	"unsupported_request":      "The request uses capabilities not supported by this model (e.g. streaming or tools). Check the provider capabilities.",
	"provider_key_required":    "No API key could be resolved for this provider. Set the env var referenced in the provider's api_key config field.",
	"invalid_execution_token":  "Execution token is missing or invalid.",
	"invalid_provider_config":  "Provider config is invalid. Check the provider base_url and type in virgil.toml.",
	"trace_unavailable":        "Failed to generate a trace ID.",
	"run_id_unavailable":       "Failed to resolve or generate a run ID.",
	"policy_unavailable":       "Policy engine is unavailable. The gateway may still be starting.",
	"provider_transport_error": "Could not reach the upstream provider. Check network connectivity and the provider base_url.",
	"provider_response_error":  "The upstream provider returned an unexpected response format.",
}

func writeAPIError(w http.ResponseWriter, status int, code string) {
	msg := errorMessages[code]
	if msg == "" {
		msg = http.StatusText(status)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"message": msg,
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
	if err := selected.validate(body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "unsupported_request")
		return
	}
	identity, supervised := identityFromRequest(req)
	var key string
	if supervised && identity.UseConfiguredKey {
		key = router.getenv(selected.keyEnv)
		ok = selected.keyEnv != "" && key != ""
	} else {
		key, ok = router.resolveKey(req.Header.Get("Authorization"), selected.keyEnv)
	}
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "provider_key_required")
		return
	}
	runID := identity.RunID
	if !supervised {
		var err error
		runID, err = telemetry.ResolveRunID(req.Header.Get("X-Virgil-Run-ID"))
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "run_id_unavailable")
			return
		}
	}
	slog.Info("→ request", "provider", selected.provider, "model", selector.Model, "stream", selector.Stream, "run_id", runID)
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
	w.Header().Set("X-Virgil-Run-ID", runID)
	upstreamReq.Header.Set("traceparent", "00-"+trace.TraceID+"-"+trace.SpanID+"-01")
	started := time.Now()
	result := providers.Result{Status: "transport_error", UsageSource: "unknown", ErrorCode: "provider_transport_error"}
	var policyDecision *telemetry.PolicyDecision
	var reservationID string
	defer func() {
		slog.Info("← response",
			"status", result.Status,
			"provider", selected.provider,
			"model", selector.Model,
			"in_tokens", result.InputTokens,
			"out_tokens", result.OutputTokens,
			"duration_ms", time.Since(started).Milliseconds(),
			"error_code", result.ErrorCode,
		)
		u := telemetry.FromResult(result.UsageSource, result.InputTokens, result.OutputTokens, result.CachedTokens)
		var actualCost, estimatedCost *string
		var pricingVersion, costCurrency string
		if entry, ok := router.pricingCfg.Models[result.ResponseModel]; ok && entry.InputPerToken != "" {
			table := pricing.Table{
				Version: router.pricingCfg.Version, Currency: router.pricingCfg.Currency,
				InputPerToken: entry.InputPerToken, OutputPerToken: entry.OutputPerToken,
				CachedPerToken: entry.CachedPerToken,
			}
			if c, err := pricing.Calculate(u, table); err == nil {
				actualCost, estimatedCost = c.ActualCost, c.EstimatedCost
				pricingVersion, costCurrency = router.pricingCfg.Version, router.pricingCfg.Currency
			}
		} else if router.priceRegistry != nil {
			// Fall back to the price registry for provider:model lookups.
			if pe, ok := router.priceRegistry.Lookup(selected.provider, result.ResponseModel); ok && pe.InputPerToken != "" {
				table := pricing.Table{
					Version:        router.pricingCfg.Version,
					Currency:       router.pricingCfg.Currency,
					InputPerToken:  pe.InputPerToken,
					OutputPerToken: pe.OutputPerToken,
					CachedPerToken: pe.CachedPerToken,
				}
				if c, err := pricing.Calculate(u, table); err == nil {
					actualCost, estimatedCost = c.ActualCost, c.EstimatedCost
					pricingVersion, costCurrency = router.pricingCfg.Version, router.pricingCfg.Currency
				}
			}
		}
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
		cost := ""
		if actualCost != nil {
			cost = *actualCost
		} else if estimatedCost != nil {
			cost = *estimatedCost
		}
		if router.recorder == nil {
			// Still need to postflight even without a recorder.
			if router.policy != nil && reservationID != "" {
				toolCalls := int64(len(result.ToolCallFingerprints))
				ctx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), 2*time.Second)
				if err := router.policy.Postflight(ctx, policies.OutcomeFacts{RunID: runID, ReservationID: reservationID, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, CostUSD: cost, ToolCalls: &toolCalls}); err != nil {
					slog.Error("policy reconciliation failed", "code", "policy_postflight_failed")
				}
				cancel()
			}
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
			ActualCost:     actualCost,
			EstimatedCost:  estimatedCost,
			PricingVersion: pricingVersion,
			CostCurrency:   costCurrency,
			StartedAt:      started,
			EndedAt:        time.Now(),
		})
		if err != nil {
			slog.Error("event rejected", "code", "event_build_failed")
			// Postflight must still run to release the reservation even if telemetry failed.
			if router.policy != nil && reservationID != "" {
				toolCalls := int64(len(result.ToolCallFingerprints))
				pfCtx, pfCancel := context.WithTimeout(context.WithoutCancel(req.Context()), 2*time.Second)
				if pfErr := router.policy.Postflight(pfCtx, policies.OutcomeFacts{RunID: runID, ReservationID: reservationID, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, CostUSD: cost, ToolCalls: &toolCalls}); pfErr != nil {
					slog.Error("policy reconciliation failed", "code", "policy_postflight_failed")
				}
				pfCancel()
			}
			return
		}
		event, err = redaction.Prepare(event)
		if err != nil {
			slog.Error("event rejected", "code", "event_redaction_failed")
			// Postflight must still run to release the reservation even if redaction failed.
			if router.policy != nil && reservationID != "" {
				toolCalls := int64(len(result.ToolCallFingerprints))
				pfCtx, pfCancel := context.WithTimeout(context.WithoutCancel(req.Context()), 2*time.Second)
				if pfErr := router.policy.Postflight(pfCtx, policies.OutcomeFacts{RunID: runID, ReservationID: reservationID, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, CostUSD: cost, ToolCalls: &toolCalls}); pfErr != nil {
					slog.Error("policy reconciliation failed", "code", "policy_postflight_failed")
				}
				pfCancel()
			}
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), 2*time.Second)
		defer cancel()
		// Save prompt/response content if the recorder supports it.
		if cs, ok := router.recorder.(ContentSaver); ok && (router.privacy.CapturePrompts || router.privacy.CaptureResponses) {
			var promptJSON string
			if router.privacy.CapturePrompts {
				var msgHolder struct {
					Messages json.RawMessage `json:"messages"`
				}
				if json.Unmarshal(body, &msgHolder) == nil && len(msgHolder.Messages) > 0 {
					promptJSON = string(msgHolder.Messages)
				}
			}
			responseText, errorMessage := "", ""
			if router.privacy.CaptureResponses {
				responseText = result.ResponseText
				errorMessage = result.ErrorBody
			}
			_ = cs.SaveContent(ctx, storage.ContentRecord{
				EventID:      event.EventID,
				PromptJSON:   promptJSON,
				ResponseText: responseText,
				ErrorMessage: errorMessage,
				CreatedAtNS:  event.CreatedAt.UnixNano(),
			})
		}
		// If recorder supports combined postflight+append, use one transaction.
		if pr, ok := router.recorder.(PostflightRecorder); ok && router.policy != nil && reservationID != "" {
			toolCalls := int64(len(result.ToolCallFingerprints))
			outcome := storage.PolicyOutcome{
				RunID: runID, ReservationID: reservationID,
				ToolCalls: toolCalls, CostUSD: cost,
			}
			if result.InputTokens != nil {
				outcome.InputTokens = *result.InputTokens
			} else {
				outcome.InputTokens = -1
			}
			if result.OutputTokens != nil {
				outcome.OutputTokens = *result.OutputTokens
			} else {
				outcome.OutputTokens = -1
			}
			if err := pr.PostflightAndAppend(ctx, outcome, event, nil, cost); err != nil {
				slog.Error("postflight+append failed", "code", "journal_write_failed")
			}
			return
		}
		// Fallback: separate postflight then record (e.g. control-plane outbox recorder).
		if router.policy != nil && reservationID != "" {
			toolCalls := int64(len(result.ToolCallFingerprints))
			if err := router.policy.Postflight(ctx, policies.OutcomeFacts{RunID: runID, ReservationID: reservationID, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, CostUSD: cost, ToolCalls: &toolCalls}); err != nil {
				slog.Error("policy reconciliation failed", "code", "policy_postflight_failed")
			}
		}
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

// listModels handles GET /v1/models — returns the models configured in this gateway.
func (router *router) listModels(w http.ResponseWriter, _ *http.Request) {
	type modelObj struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	data := make([]modelObj, 0, len(router.models))
	for id, r := range router.models {
		data = append(data, modelObj{ID: id, Object: "model", OwnedBy: r.provider})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
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
	// If the provider has a configured key, always use it (local/personal mode).
	// VIRGIL_LOCAL_APP_TOKEN enables multi-tenant mode: clients authenticate with
	// a shared local token and Virgil swaps in the real provider key.
	if providerKeyEnv != "" {
		if providerKey := router.getenv(providerKeyEnv); providerKey != "" {
			localToken := router.getenv("VIRGIL_LOCAL_APP_TOKEN")
			if localToken == "" || subtle.ConstantTimeCompare([]byte(presented), []byte(localToken)) == 1 {
				return providerKey, true
			}
		}
	}
	return presented, true
}

package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/policies"
	"github.com/MiguelReis944/Virgil/internal/pricing"
	"github.com/MiguelReis944/Virgil/internal/providers"
	"github.com/MiguelReis944/Virgil/internal/redaction"
	"github.com/MiguelReis944/Virgil/internal/storage"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

const (
	maxResponsesRequest = 8 << 20
	maxResponsesBody    = 16 << 20
)

// Responses deliberately supports only stateless, foreground calls. In particular,
// previous_response_id would allow upstream context outside Virgil's inspection.
type responsesRequest struct {
	Model              string            `json:"model"`
	Input              json.RawMessage   `json:"input"`
	Stream             bool              `json:"stream"`
	Store              *bool             `json:"store"`
	Background         bool              `json:"background"`
	PreviousResponseID string            `json:"previous_response_id"`
	Tools              []json.RawMessage `json:"tools"`
	Instructions       json.RawMessage   `json:"instructions"`
	ToolChoice         json.RawMessage   `json:"tool_choice"`
	ParallelToolCalls  *bool             `json:"parallel_tool_calls"`
	MaxOutputTokens    *int64            `json:"max_output_tokens"`
	Reasoning          json.RawMessage   `json:"reasoning"`
	Text               json.RawMessage   `json:"text"`
	Include            json.RawMessage   `json:"include"`
	Metadata           json.RawMessage   `json:"metadata"`
	Truncation         string            `json:"truncation"`
	Temperature        *float64          `json:"temperature"`
	TopP               *float64          `json:"top_p"`
	User               string            `json:"user"`
	PromptCacheKey     string            `json:"prompt_cache_key"`
	SafetyIdentifier   string            `json:"safety_identifier"`
	ServiceTier        string            `json:"service_tier"`
	ClientMetadata     json.RawMessage   `json:"client_metadata"`
}

func parseResponsesRequest(body []byte) (responsesRequest, []string, error) {
	var input responsesRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return input, nil, err
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return input, nil, errors.New("extra JSON value")
	}
	if input.Model == "" || len(input.Input) == 0 || string(input.Input) == "null" {
		return input, nil, errors.New("model and input are required")
	}
	if input.Background || input.PreviousResponseID != "" {
		return input, nil, errors.New("stateful and background requests are unsupported")
	}
	if input.Store != nil && *input.Store {
		return input, nil, errors.New("store=true is unsupported")
	}
	if input.MaxOutputTokens != nil && *input.MaxOutputTokens < 0 {
		return input, nil, errors.New("negative token limit")
	}
	var names []string
	for _, raw := range input.Tools {
		var tool struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			return input, nil, err
		}
		switch tool.Type {
		case "function":
			if tool.Name == "" {
				return input, nil, errors.New("unnamed function tool")
			}
			names = append(names, tool.Name)
		case "namespace":
			if tool.Name == "" || len(tool.Tools) == 0 {
				return input, nil, errors.New("invalid namespace tool")
			}
			for _, nested := range tool.Tools {
				if nested.Name == "" {
					return input, nil, errors.New("unnamed namespace tool")
				}
				names = append(names, tool.Name+"."+nested.Name)
			}
		case "web_search":
			names = append(names, "web_search")
		default:
			return input, nil, errors.New("unsupported tool type")
		}
	}
	return input, names, nil
}

func (router *router) responses(w http.ResponseWriter, req *http.Request) {
	req.Body = http.MaxBytesReader(w, req.Body, maxResponsesRequest)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		var sizeErr *http.MaxBytesError
		var netErr net.Error
		switch {
		case errors.As(err, &sizeErr):
			writeAPIError(w, http.StatusRequestEntityTooLarge, "responses_request_too_large")
		case errors.As(err, &netErr) && netErr.Timeout():
			writeAPIError(w, http.StatusRequestTimeout, "request_timeout")
		default:
			writeAPIError(w, http.StatusBadRequest, "invalid_request")
		}
		return
	}
	input, toolNames, err := parseResponsesRequest(body)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "unsupported_request")
		return
	}
	// Codex sends local session identifiers here. They are not part of the
	// public Responses request and need not be disclosed to the provider.
	var upstreamFields map[string]json.RawMessage
	if err := json.Unmarshal(body, &upstreamFields); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	delete(upstreamFields, "client_metadata")
	if input.Store == nil {
		upstreamFields["store"] = json.RawMessage("false")
	}
	body, err = json.Marshal(upstreamFields)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	selected, ok := router.models[input.Model]
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "model_not_configured")
		return
	}
	if selected.providerType != "openai" && selected.providerType != "openai-compatible" {
		writeAPIError(w, http.StatusBadRequest, "unsupported_request")
		return
	}
	identity, supervised := identityFromRequest(req)
	var key string
	if identity.UseConfiguredKey {
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
	if runID == "desktop_codex" {
		var metadata struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(input.ClientMetadata, &metadata); err == nil {
			runID = desktopSessionRunID("codex", metadata.SessionID)
		}
	}
	if runID == "" {
		runID, err = telemetry.ResolveRunID(req.Header.Get("X-Virgil-Run-ID"))
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "run_id_unavailable")
			return
		}
	}
	trace, err := telemetry.NewTrace(req.Header.Get("traceparent"))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "trace_unavailable")
		return
	}
	base, err := url.Parse(selected.baseURL)
	if err != nil || base.Host == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_provider_config")
		return
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/responses"
	base.RawQuery, base.Fragment = "", ""
	upstream, err := http.NewRequestWithContext(req.Context(), http.MethodPost, base.String(), bytes.NewReader(body))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_provider_config")
		return
	}
	upstream.Header.Set("Authorization", "Bearer "+key)
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("traceparent", "00-"+trace.TraceID+"-"+trace.SpanID+"-01")
	w.Header().Set("X-Virgil-Run-ID", runID)
	started := time.Now()
	result := providers.Result{Status: "transport_error", UsageSource: "unknown", ErrorCode: "provider_transport_error"}
	var reservationID string
	finalizedBlock := false
	defer func() {
		if !finalizedBlock {
			router.recordResponsesOutcome(req.Context(), runID, trace, selected, input.Model, started, reservationID, result)
		}
	}()
	if router.policy != nil {
		var estimateInput, estimateOutput *int64
		if router.guardrails.EstimatedInputTokensPerCall > 0 {
			estimateInput = &router.guardrails.EstimatedInputTokensPerCall
		}
		if router.guardrails.EstimatedOutputTokensPerCall > 0 {
			estimateOutput = &router.guardrails.EstimatedOutputTokensPerCall
		}
		decision, preErr := router.policy.Preflight(req.Context(), policies.RequestFacts{
			RunID: runID, Provider: selected.provider, Model: input.Model, ToolNames: toolNames, StartedAt: started,
			EstimatedCostUSD:     router.guardrails.EstimatedCostPerCallUSD,
			EstimatedInputTokens: estimateInput, EstimatedOutputTokens: estimateOutput,
		})
		if preErr != nil {
			result.ErrorCode = "policy_unavailable"
			writeAPIError(w, http.StatusServiceUnavailable, "policy_unavailable")
			return
		}
		if decision.Decision == "block" {
			policyDecision := &telemetry.PolicyDecision{Decision: decision.Decision, Reason: decision.Reason, Policy: decision.Policy, Attempt: decision.Attempt, Threshold: decision.Threshold}
			notice, notify := router.finalizePolicyBlock(req.Context(), telemetry.Attempt{
				InstallationID: router.installationID, RunID: runID, TraceID: trace.TraceID, SpanID: trace.SpanID,
				Provider: selected.provider, RequestedModel: input.Model, UsageSource: "unknown", Status: "policy_block",
				ErrorCode: decision.Reason, PolicyDecision: policyDecision, StartedAt: started, EndedAt: time.Now(),
			}, decision, supervised)
			finalizedBlock = true
			writePolicyBlock(w, decision)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			if supervised && notify && router.policyNotifier != nil {
				router.policyNotifier.NotifyPolicyBlock(notice)
			}
			return
		}
		reservationID = decision.ReservationID
	}
	resp, err := selected.adapter.Do(upstream)
	if err != nil {
		if req.Context().Err() != nil {
			result.Status, result.ErrorCode = "client_cancelled", "client_cancelled"
		}
		writeAPIError(w, http.StatusBadGateway, "provider_transport_error")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Status, result.ErrorCode = "provider_error", "provider_http_"+http.StatusText(resp.StatusCode)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "provider request failed", "type": "provider_error", "code": "provider_error"}})
		return
	}
	if input.Stream {
		result, err = streamResponses(req.Context(), resp.Body, w)
		if err != nil {
			slog.Warn("Responses stream failed", "code", "provider_stream_error")
		}
		return
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponsesBody+1))
	if err != nil || len(raw) > maxResponsesBody {
		result.Status, result.ErrorCode = "provider_error", "provider_response_error"
		writeAPIError(w, http.StatusBadGateway, "provider_response_error")
		return
	}
	result, err = inspectResponsesPayload(raw)
	if err != nil {
		result.Status, result.ErrorCode = "provider_error", "provider_response_error"
		writeAPIError(w, http.StatusBadGateway, "provider_response_error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func inspectResponsesPayload(raw []byte) (providers.Result, error) {
	result := providers.Result{Status: "success", UsageSource: "unknown"}
	var payload struct {
		Model  string `json:"model"`
		Status string `json:"status"`
		Usage  *struct {
			InputTokens        int64 `json:"input_tokens"`
			OutputTokens       int64 `json:"output_tokens"`
			InputTokensDetails *struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
		Output []struct {
			Type      string `json:"type"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return result, err
	}
	if payload.Status != "completed" || payload.Model == "" {
		return result, errors.New("incomplete response")
	}
	result.ResponseModel = payload.Model
	if payload.Usage != nil {
		if payload.Usage.InputTokens < 0 || payload.Usage.OutputTokens < 0 {
			return result, errors.New("negative usage")
		}
		result.InputTokens, result.OutputTokens = &payload.Usage.InputTokens, &payload.Usage.OutputTokens
		if payload.Usage.InputTokensDetails != nil {
			cached := payload.Usage.InputTokensDetails.CachedTokens
			if cached < 0 || cached > payload.Usage.InputTokens {
				return result, errors.New("invalid cached usage")
			}
			result.CachedTokens = &cached
		}
		result.UsageSource = "provider"
	}
	for _, item := range payload.Output {
		if item.Type == "function_call" || item.Type == "custom_tool_call" {
			fingerprint := sha256.Sum256([]byte(item.Name + "\x00" + item.Arguments))
			result.ToolCallFingerprints = append(result.ToolCallFingerprints, hex.EncodeToString(fingerprint[:]))
		}
	}
	return result, nil
}

func readResponsesLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > 1<<20 {
			return nil, errors.New("Responses SSE line exceeds limit")
		}
		line = append(line, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}

func writeResponsesStreamError(w http.ResponseWriter) {
	_, _ = io.WriteString(w, "event: error\ndata: {\"error\":{\"type\":\"provider_error\",\"code\":\"provider_stream_error\",\"message\":\"provider stream failed\"}}\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func streamResponses(ctx context.Context, body io.Reader, w http.ResponseWriter) (providers.Result, error) {
	result := providers.Result{Status: "transport_error", UsageSource: "unknown", ErrorCode: "provider_stream_error"}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	reader := bufio.NewReader(body)
	var frame []byte
	var total int
	for {
		if err := ctx.Err(); err != nil {
			result.Status, result.ErrorCode = "client_cancelled", "client_cancelled"
			return result, err
		}
		line, err := readResponsesLine(reader)
		if len(line) > 0 {
			total += len(line)
			if len(frame)+len(line) > 1<<20 || total > maxResponsesBody {
				writeResponsesStreamError(w)
				return result, errors.New("Responses stream exceeds limit")
			}
			frame = append(frame, line...)
			if len(bytes.TrimSpace(line)) == 0 {
				for _, part := range bytes.Split(frame, []byte("\n")) {
					if bytes.HasPrefix(part, []byte("data:")) {
						var evt struct {
							Type     string          `json:"type"`
							Response json.RawMessage `json:"response"`
						}
						if json.Unmarshal(bytes.TrimSpace(part[5:]), &evt) == nil {
							if evt.Type == "response.completed" {
								parsed, parseErr := inspectResponsesPayload(evt.Response)
								if parseErr != nil {
									writeResponsesStreamError(w)
									return result, parseErr
								}
								result = parsed
							}
							if evt.Type == "response.failed" {
								result.Status, result.ErrorCode = "provider_error", "provider_response_error"
								writeResponsesStreamError(w)
								return result, errors.New("provider response failed")
							}
						}
					}
				}
				if _, writeErr := w.Write(frame); writeErr != nil {
					result.Status, result.ErrorCode = "client_cancelled", "client_cancelled"
					return result, writeErr
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				frame = frame[:0]
				if result.Status == "success" {
					return result, nil
				}
			}
		}
		if err != nil {
			writeResponsesStreamError(w)
			return result, err
		}
	}
}

func (router *router) recordResponsesOutcome(ctx context.Context, runID string, trace telemetry.TraceContext, selected route, model string, started time.Time, reservationID string, result providers.Result) {
	if router.policy != nil && reservationID != "" && (result.Status == "success" || result.Status == "provider_error" || result.Status == "transport_error") {
		recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		_ = router.policy.RecordProviderOutcome(recordCtx, runID, result.Status, result.ErrorCode)
		cancel()
	}
	if router.policy != nil && reservationID != "" && len(result.ToolCallFingerprints) > 0 {
		recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		_ = router.policy.RecordToolCalls(recordCtx, runID, result.ToolCallFingerprints)
		cancel()
	}
	var cost string
	var actualCost *string
	var estimatedCost *string
	var pricingVersion, costCurrency string
	if result.UsageSource == "provider" {
		entry, ok := router.pricingCfg.Models[result.ResponseModel]
		if !ok && router.priceRegistry != nil {
			if priced, exists := router.priceRegistry.Lookup(selected.provider, result.ResponseModel); exists {
				entry.InputPerToken, entry.OutputPerToken, entry.CachedPerToken = priced.InputPerToken, priced.OutputPerToken, priced.CachedPerToken
				ok = true
			}
		}
		if ok && entry.InputPerToken != "" {
			if c, err := pricing.Calculate(telemetry.FromResult(result.UsageSource, result.InputTokens, result.OutputTokens, result.CachedTokens), pricing.Table{Version: router.pricingCfg.Version, Currency: router.pricingCfg.Currency, InputPerToken: entry.InputPerToken, OutputPerToken: entry.OutputPerToken, CachedPerToken: entry.CachedPerToken}); err == nil {
				actualCost = c.ActualCost
				pricingVersion, costCurrency = router.pricingCfg.Version, router.pricingCfg.Currency
				if actualCost != nil {
					cost = *actualCost
				}
			}
		}
	}
	if cost == "" && router.guardrails.EstimatedCostPerCallUSD != "" && result.Status == "success" {
		cost = router.guardrails.EstimatedCostPerCallUSD
		estimatedCost = &cost
		result.UsageSource = "estimated"
	}
	toolCalls := int64(len(result.ToolCallFingerprints))
	outcome := policies.OutcomeFacts{RunID: runID, ReservationID: reservationID, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, CostUSD: cost, ToolCalls: &toolCalls}
	event, err := telemetry.Build(telemetry.Attempt{InstallationID: router.installationID, RunID: runID, TraceID: trace.TraceID, SpanID: trace.SpanID, Provider: selected.provider, RequestedModel: model, ResponseModel: result.ResponseModel, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, CachedTokens: result.CachedTokens, UsageSource: result.UsageSource, Status: result.Status, ErrorCode: result.ErrorCode, ActualCost: actualCost, EstimatedCost: estimatedCost, PricingVersion: pricingVersion, CostCurrency: costCurrency, StartedAt: started, EndedAt: time.Now()})
	if err == nil {
		event, err = redaction.Prepare(event)
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err == nil && router.recorder != nil {
		if pr, ok := router.recorder.(PostflightRecorder); ok && router.policy != nil && reservationID != "" {
			storageOutcome := storage.PolicyOutcome{RunID: runID, ReservationID: reservationID, CostUSD: cost, ToolCalls: toolCalls, InputTokens: -1, OutputTokens: -1}
			if result.InputTokens != nil {
				storageOutcome.InputTokens = *result.InputTokens
			}
			if result.OutputTokens != nil {
				storageOutcome.OutputTokens = *result.OutputTokens
			}
			if e := pr.PostflightAndAppend(writeCtx, storageOutcome, event, nil, cost); e != nil {
				slog.Error("Responses postflight+append failed", "code", "journal_write_failed")
			}
			return
		}
	}
	if router.policy != nil && reservationID != "" {
		if e := router.policy.Postflight(writeCtx, outcome); e != nil {
			slog.Error("Responses postflight failed", "code", "policy_postflight_failed")
		}
	}
	if err == nil && router.recorder != nil {
		if e := router.recorder.Record(writeCtx, event); e != nil {
			slog.Error("Responses event record failed", "code", "journal_write_failed")
		}
	}
}

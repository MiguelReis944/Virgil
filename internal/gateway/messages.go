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
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/policies"
	"github.com/MiguelReis944/Virgil/internal/providers"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

const maxMessagesBody = 16 << 20

type messagesRequest struct {
	Model     string          `json:"model"`
	MaxTokens int64           `json:"max_tokens"`
	Messages  json.RawMessage `json:"messages"`
	Stream    bool            `json:"stream"`
	Tools     []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"tools"`
}

// Keep the native Messages surface explicit: new server-side capabilities must be
// assessed before they can bypass the local policy engine.
var allowedMessagesFields = map[string]bool{
	"model": true, "max_tokens": true, "messages": true, "stream": true, "system": true,
	"tools": true, "tool_choice": true, "temperature": true, "top_p": true, "top_k": true,
	"stop_sequences": true, "metadata": true, "thinking": true, "output_config": true,
	"service_tier": true,
}

func parseMessagesRequest(body []byte) (messagesRequest, []string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return messagesRequest{}, nil, errors.New("invalid JSON object")
	}
	for field := range fields {
		if !allowedMessagesFields[field] {
			return messagesRequest{}, nil, errors.New("unsupported Messages field")
		}
	}
	var input messagesRequest
	if err := json.Unmarshal(body, &input); err != nil || input.Model == "" || input.MaxTokens <= 0 || len(input.Messages) == 0 || string(input.Messages) == "null" {
		return input, nil, errors.New("invalid Messages request")
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(input.Messages, &messages); err != nil || len(messages) == 0 {
		return input, nil, errors.New("messages required")
	}
	names := make([]string, 0, len(input.Tools))
	for _, tool := range input.Tools {
		if tool.Name == "" || (tool.Type != "" && tool.Type != "custom") {
			return input, nil, errors.New("unsupported tool")
		}
		names = append(names, tool.Name)
	}
	return input, names, nil
}

func (router *router) authenticateMessages(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		key := req.Header.Get("x-api-key")
		bearer := req.Header.Get("Authorization")
		if key != "" && bearer != "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		router.authenticateExecution(next, "Authorization")(w, req)
	}
}

func (router *router) messages(w http.ResponseWriter, req *http.Request) {
	if req.URL.RawQuery != "" && req.URL.RawQuery != "beta=true" {
		writeAPIError(w, http.StatusBadRequest, "unsupported_request")
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxResponsesRequest)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "responses_request_too_large")
		return
	}
	input, tools, err := parseMessagesRequest(body)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "unsupported_request")
		return
	}
	selected, ok := router.models[input.Model]
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "model_not_configured")
		return
	}
	if selected.providerType != "anthropic" {
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
	base.Path = strings.TrimRight(base.Path, "/") + "/messages"
	base.RawQuery = req.URL.RawQuery
	base.Fragment = ""
	upstream, err := http.NewRequestWithContext(req.Context(), http.MethodPost, base.String(), bytes.NewReader(body))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_provider_config")
		return
	}
	upstream.Header.Set("x-api-key", key)
	upstream.Header.Set("anthropic-version", req.Header.Get("anthropic-version"))
	if upstream.Header.Get("anthropic-version") == "" {
		upstream.Header.Set("anthropic-version", "2023-06-01")
	}
	if beta := req.Header.Get("anthropic-beta"); beta != "" {
		upstream.Header.Set("anthropic-beta", beta)
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("traceparent", "00-"+trace.TraceID+"-"+trace.SpanID+"-01")
	w.Header().Set("X-Virgil-Run-ID", runID)
	started := time.Now()
	result := providers.Result{Status: "transport_error", UsageSource: "unknown", ErrorCode: "provider_transport_error"}
	var reservationID string
	finalized := false
	defer func() {
		if !finalized {
			router.recordResponsesOutcome(req.Context(), runID, trace, selected, input.Model, started, reservationID, result)
		}
	}()
	if router.policy != nil {
		var estimatedInput, estimatedOutput *int64
		if router.guardrails.EstimatedInputTokensPerCall > 0 {
			estimatedInput = &router.guardrails.EstimatedInputTokensPerCall
		}
		if router.guardrails.EstimatedOutputTokensPerCall > 0 {
			estimatedOutput = &router.guardrails.EstimatedOutputTokensPerCall
		}
		decision, e := router.policy.Preflight(req.Context(), policies.RequestFacts{RunID: runID, Provider: selected.provider, Model: input.Model, ToolNames: tools, StartedAt: started, EstimatedCostUSD: router.guardrails.EstimatedCostPerCallUSD, EstimatedInputTokens: estimatedInput, EstimatedOutputTokens: estimatedOutput})
		if e != nil {
			result.ErrorCode = "policy_unavailable"
			writeAPIError(w, http.StatusServiceUnavailable, "policy_unavailable")
			return
		}
		if decision.Decision == "block" {
			policyDecision := &telemetry.PolicyDecision{Decision: decision.Decision, Reason: decision.Reason, Policy: decision.Policy, Attempt: decision.Attempt, Threshold: decision.Threshold}
			notice, notify := router.finalizePolicyBlock(req.Context(), telemetry.Attempt{InstallationID: router.installationID, RunID: runID, TraceID: trace.TraceID, SpanID: trace.SpanID, Provider: selected.provider, RequestedModel: input.Model, UsageSource: "unknown", Status: "policy_block", ErrorCode: decision.Reason, PolicyDecision: policyDecision, StartedAt: started, EndedAt: time.Now()}, decision, supervised)
			finalized = true
			writePolicyBlock(w, decision)
			if supervised && notify && router.policyNotifier != nil {
				router.policyNotifier.NotifyPolicyBlock(notice)
			}
			return
		}
		reservationID = decision.ReservationID
	}
	resp, err := selected.adapter.Do(upstream)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "provider_transport_error")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Status = "provider_error"
		result.ErrorCode = "provider_http_error"
		writeAPIError(w, resp.StatusCode, "provider_response_error")
		return
	}
	if input.Stream {
		result, err = streamMessages(req.Context(), resp.Body, w)
		if err != nil {
			return
		}
		return
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxMessagesBody+1))
	if err != nil || len(raw) > maxMessagesBody {
		result.Status = "provider_error"
		result.ErrorCode = "provider_response_error"
		writeAPIError(w, http.StatusBadGateway, "provider_response_error")
		return
	}
	result, err = inspectMessagesPayload(raw)
	if err != nil {
		result.Status = "provider_error"
		result.ErrorCode = "provider_response_error"
		writeAPIError(w, http.StatusBadGateway, "provider_response_error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func inspectMessagesPayload(raw []byte) (providers.Result, error) {
	result := providers.Result{Status: "success", UsageSource: "unknown"}
	var payload struct {
		Model string `json:"model"`
		Usage struct {
			Input         int64 `json:"input_tokens"`
			Output        int64 `json:"output_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
			CacheCreation int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		Content []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Model == "" || payload.Usage.Input < 0 || payload.Usage.Output < 0 || payload.Usage.CacheRead < 0 || payload.Usage.CacheCreation < 0 || payload.Usage.Input > math.MaxInt64-payload.Usage.CacheRead || payload.Usage.Input+payload.Usage.CacheRead > math.MaxInt64-payload.Usage.CacheCreation {
		return result, errors.New("invalid provider Messages response")
	}
	payload.Usage.Input += payload.Usage.CacheRead + payload.Usage.CacheCreation
	result.ResponseModel = payload.Model
	result.InputTokens = &payload.Usage.Input
	result.OutputTokens = &payload.Usage.Output
	result.CachedTokens = &payload.Usage.CacheRead
	result.UsageSource = "provider"
	for _, block := range payload.Content {
		if block.Type == "tool_use" {
			fingerprint := sha256.Sum256([]byte(block.Name + "\x00" + string(block.Input)))
			result.ToolCallFingerprints = append(result.ToolCallFingerprints, hex.EncodeToString(fingerprint[:]))
		}
	}
	return result, nil
}

func streamMessages(ctx context.Context, body io.Reader, w http.ResponseWriter) (providers.Result, error) {
	result := providers.Result{Status: "transport_error", UsageSource: "unknown", ErrorCode: "provider_stream_error"}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	reader := bufio.NewReader(body)
	var frame []byte
	total := 0
	var model string
	var input, output, cache, cacheCreation int64
	var tools []string
	var currentName string
	var currentInput bytes.Buffer
	for {
		if err := ctx.Err(); err != nil {
			result.Status = "client_cancelled"
			result.ErrorCode = "client_cancelled"
			return result, err
		}
		line, err := readResponsesLine(reader)
		if len(line) > 0 {
			total += len(line)
			if total > maxMessagesBody || len(frame)+len(line) > 1<<20 {
				return result, errors.New("Messages stream exceeds limit")
			}
			frame = append(frame, line...)
			if len(bytes.TrimSpace(line)) == 0 {
				for _, part := range bytes.Split(frame, []byte("\n")) {
					if !bytes.HasPrefix(part, []byte("data:")) {
						continue
					}
					var evt struct {
						Type    string `json:"type"`
						Message struct {
							Model string `json:"model"`
							Usage struct {
								Input         int64 `json:"input_tokens"`
								CacheRead     int64 `json:"cache_read_input_tokens"`
								CacheCreation int64 `json:"cache_creation_input_tokens"`
							} `json:"usage"`
						} `json:"message"`
						Usage struct {
							Output int64 `json:"output_tokens"`
						} `json:"usage"`
						ContentBlock struct {
							Type  string          `json:"type"`
							Name  string          `json:"name"`
							Input json.RawMessage `json:"input"`
						} `json:"content_block"`
						Delta struct {
							Type        string `json:"type"`
							PartialJSON string `json:"partial_json"`
						} `json:"delta"`
					}
					if json.Unmarshal(bytes.TrimSpace(part[5:]), &evt) != nil {
						continue
					}
					switch evt.Type {
					case "message_start":
						model = evt.Message.Model
						input = evt.Message.Usage.Input
						cache = evt.Message.Usage.CacheRead
						cacheCreation = evt.Message.Usage.CacheCreation
					case "message_delta":
						output = evt.Usage.Output
					case "content_block_start":
						if evt.ContentBlock.Type == "tool_use" {
							currentName = evt.ContentBlock.Name
							currentInput.Reset()
							currentInput.Write(evt.ContentBlock.Input)
						}
					case "content_block_delta":
						if evt.Delta.Type == "input_json_delta" {
							currentInput.WriteString(evt.Delta.PartialJSON)
						}
					case "content_block_stop":
						if currentName != "" {
							fp := sha256.Sum256([]byte(currentName + "\x00" + currentInput.String()))
							tools = append(tools, hex.EncodeToString(fp[:]))
							currentName = ""
						}
					case "message_stop":
						if model == "" || input < 0 || output < 0 || cache < 0 || cacheCreation < 0 || input > math.MaxInt64-cache || input+cache > math.MaxInt64-cacheCreation {
							return result, errors.New("invalid Messages stream")
						}
						input += cache + cacheCreation
						result = providers.Result{Status: "success", UsageSource: "provider", ResponseModel: model, InputTokens: &input, OutputTokens: &output, CachedTokens: &cache, ToolCallFingerprints: tools}
					case "error":
						result.Status = "provider_error"
						result.ErrorCode = "provider_stream_error"
						return result, errors.New("provider stream error")
					}
				}
				if _, writeErr := w.Write(frame); writeErr != nil {
					result.Status = "client_cancelled"
					result.ErrorCode = "client_cancelled"
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
			return result, err
		}
	}
}

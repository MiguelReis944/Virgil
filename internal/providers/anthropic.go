package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const anthropicVersion = "2023-06-01"

type Anthropic struct {
	baseURL string
	client  *http.Client
}

func NewAnthropic(baseURL string, client *http.Client) *Anthropic {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Anthropic{baseURL: baseURL, client: &noRedirect}
}

type anthropicRequest struct {
	Model         string   `json:"model"`
	Messages      []any    `json:"messages"`
	System        string   `json:"system,omitempty"`
	MaxTokens     int64    `json:"max_tokens"`
	Stream        bool     `json:"stream,omitempty"`
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"top_p,omitempty"`
	StopSequences []string `json:"stop_sequences,omitempty"`
	Tools         []any    `json:"tools,omitempty"`
	ToolChoice    any      `json:"tool_choice,omitempty"`
}

func (a *Anthropic) Validate(body json.RawMessage) error {
	_, err := translateAnthropicRequest(body)
	return err
}

func translateAnthropicRequest(body json.RawMessage) (anthropicRequest, error) {
	var in chatRequest
	if err := decodeStrict(body, &in); err != nil {
		return anthropicRequest{}, fmt.Errorf("invalid chat request: %w", err)
	}
	if in.Model == "" || len(in.Messages) == 0 {
		return anthropicRequest{}, errors.New("model and messages are required")
	}
	if in.MaxTokens == nil || *in.MaxTokens <= 0 {
		return anthropicRequest{}, errors.New("max_tokens is required and must be positive")
	}
	out := anthropicRequest{Model: in.Model, Stream: in.Stream, Temperature: in.Temperature, TopP: in.TopP, MaxTokens: *in.MaxTokens}
	if len(in.StreamOptions) > 0 {
		var opts struct {
			IncludeUsage bool `json:"include_usage"`
		}
		if !in.Stream || decodeStrict(in.StreamOptions, &opts) != nil {
			return out, errors.New("unsupported stream_options")
		}
	}
	if len(in.Stop) > 0 && string(in.Stop) != "null" {
		var one string
		if json.Unmarshal(in.Stop, &one) == nil {
			out.StopSequences = []string{one}
		} else if json.Unmarshal(in.Stop, &out.StopSequences) != nil {
			return out, errors.New("unsupported stop")
		}
	}
	knownCalls := make(map[string]bool)
	seenConversation := false
	for _, raw := range in.Messages {
		var msg chatMessage
		if err := decodeStrict(raw, &msg); err != nil {
			return out, fmt.Errorf("unsupported message: %w", err)
		}
		var content string
		if len(msg.Content) > 0 && string(msg.Content) != "null" && json.Unmarshal(msg.Content, &content) != nil {
			return out, errors.New("only text message content is supported")
		}
		switch msg.Role {
		case "system":
			if seenConversation || len(msg.ToolCalls) > 0 || msg.ToolCallID != "" {
				return out, errors.New("unsupported system message")
			}
			if out.System != "" {
				out.System += "\n"
			}
			out.System += content
		case "user":
			seenConversation = true
			if len(msg.ToolCalls) > 0 || msg.ToolCallID != "" {
				return out, errors.New("unsupported user message")
			}
			out.Messages = append(out.Messages, map[string]any{"role": "user", "content": content})
		case "assistant":
			seenConversation = true
			if msg.ToolCallID != "" {
				return out, errors.New("unsupported assistant message")
			}
			blocks := make([]any, 0)
			if content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": content})
			}
			if len(msg.ToolCalls) > 0 {
				var calls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				}
				if json.Unmarshal(msg.ToolCalls, &calls) != nil || len(calls) == 0 {
					return out, errors.New("invalid tool calls")
				}
				for _, call := range calls {
					if call.Type != "function" || call.ID == "" || call.Function.Name == "" || knownCalls[call.ID] {
						return out, errors.New("unsupported tool call")
					}
					var args any
					if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil {
						return out, errors.New("invalid tool arguments")
					}
					if _, ok := args.(map[string]any); !ok {
						return out, errors.New("tool arguments must be an object")
					}
					blocks = append(blocks, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Function.Name, "input": args})
					knownCalls[call.ID] = true
				}
			}
			if len(blocks) == 0 {
				return out, errors.New("empty assistant message")
			}
			out.Messages = append(out.Messages, map[string]any{"role": "assistant", "content": blocks})
		case "tool":
			seenConversation = true
			if msg.ToolCallID == "" || !knownCalls[msg.ToolCallID] || len(msg.ToolCalls) > 0 {
				return out, errors.New("unmatched tool result")
			}
			out.Messages = append(out.Messages, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": msg.ToolCallID, "content": content}}})
		default:
			return out, errors.New("unsupported message role")
		}
	}
	if len(out.Messages) == 0 {
		return out, errors.New("at least one non-system message is required")
	}
	if len(in.Tools) > 0 && string(in.Tools) != "null" {
		var tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
				Strict      *bool           `json:"strict"`
			} `json:"function"`
		}
		if json.Unmarshal(in.Tools, &tools) != nil {
			return out, errors.New("invalid tools")
		}
		for _, tool := range tools {
			if tool.Type != "function" || tool.Function.Name == "" {
				return out, errors.New("unsupported tool")
			}
			if tool.Function.Strict != nil && *tool.Function.Strict {
				return out, errors.New("strict tool schemas are unsupported")
			}
			var schema any = map[string]any{"type": "object"}
			if len(tool.Function.Parameters) > 0 && string(tool.Function.Parameters) != "null" {
				if json.Unmarshal(tool.Function.Parameters, &schema) != nil {
					return out, errors.New("invalid tool schema")
				}
			}
			out.Tools = append(out.Tools, map[string]any{"name": tool.Function.Name, "description": tool.Function.Description, "input_schema": schema})
		}
	}
	if len(in.ToolChoice) > 0 && string(in.ToolChoice) != "null" {
		var choice string
		if json.Unmarshal(in.ToolChoice, &choice) == nil {
			switch choice {
			case "auto":
				out.ToolChoice = map[string]any{"type": "auto"}
			case "required":
				out.ToolChoice = map[string]any{"type": "any"}
			case "none":
				if len(out.Tools) > 0 {
					return out, errors.New("tool_choice none is unsupported with tools")
				}
			default:
				return out, errors.New("unsupported tool_choice")
			}
		} else {
			var named struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if decodeStrict(in.ToolChoice, &named) != nil || named.Type != "function" || named.Function.Name == "" {
				return out, errors.New("unsupported tool_choice")
			}
			found := false
			for _, tool := range out.Tools {
				if tool.(map[string]any)["name"] == named.Function.Name {
					found = true
					break
				}
			}
			if !found {
				return out, errors.New("tool_choice names an undefined tool")
			}
			out.ToolChoice = map[string]any{"type": "tool", "name": named.Function.Name}
		}
	}
	return out, nil
}

func (a *Anthropic) Build(ctx context.Context, body json.RawMessage, key string) (*http.Request, error) {
	translated, err := translateAnthropicRequest(body)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, errors.New("provider key is required")
	}
	base, err := url.Parse(a.baseURL)
	if err != nil || base == nil || base.Host == "" || base.User != nil || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, errors.New("invalid provider base URL")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/messages"
	base.RawQuery = ""
	base.Fragment = ""
	// Detect include_usage before translation removes it.
	var includeUsage bool
	if len(body) > 0 {
		var orig struct {
			StreamOptions *struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if json.Unmarshal(body, &orig) == nil && orig.StreamOptions != nil {
			includeUsage = orig.StreamOptions.IncludeUsage
		}
	}
	raw, err := json.Marshal(translated)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", anthropicVersion)
	if includeUsage {
		req.Header.Set("X-Virgil-Stream-Include-Usage", "true")
	}
	return req, nil
}

func (a *Anthropic) Do(req *http.Request) (*http.Response, error) {
	includeUsage := req.Header.Get("X-Virgil-Stream-Include-Usage") == "true"
	req.Header.Del("X-Virgil-Stream-Include-Usage")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	if includeUsage {
		resp.Header.Set("X-Virgil-Stream-Include-Usage", "true")
	}
	return resp, nil
}

type anthropicUsage struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
}

func applyAnthropicUsage(r *Result, u *anthropicUsage) error {
	if u == nil {
		return nil
	}
	for _, n := range []*int64{u.InputTokens, u.OutputTokens, u.CacheReadInputTokens, u.CacheCreationInputTokens} {
		if n != nil && *n < 0 {
			return errors.New("invalid provider usage")
		}
	}
	if u.InputTokens != nil {
		total := *u.InputTokens
		for _, extra := range []*int64{u.CacheReadInputTokens, u.CacheCreationInputTokens} {
			if extra != nil {
				if *extra > math.MaxInt64-total {
					return errors.New("invalid provider usage")
				}
				total += *extra
			}
		}
		r.InputTokens = &total
		r.UsageSource = "provider"
	}
	if u.OutputTokens != nil {
		r.OutputTokens = u.OutputTokens
		r.UsageSource = "provider"
	}
	if u.CacheReadInputTokens != nil {
		r.CachedTokens = u.CacheReadInputTokens
	}
	return nil
}

func (a *Anthropic) Translate(resp *http.Response, downstream http.ResponseWriter) (Result, error) {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code := fmt.Sprintf("provider_http_%d", resp.StatusCode)
		downstream.Header().Set("Content-Type", "application/json")
		downstream.WriteHeader(resp.StatusCode)
		_ = json.NewEncoder(downstream).Encode(map[string]any{"error": map[string]string{"message": "provider request failed", "type": "provider_error", "code": code}})
		return Result{ErrorCode: code, Status: "provider_error", UsageSource: "unknown"}, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderResponse+1))
	if err != nil {
		return Result{}, fmt.Errorf("read provider response: %w", err)
	}
	if len(raw) > maxProviderResponse {
		return Result{}, errors.New("provider response exceeds limit")
	}
	var msg struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string          `json:"stop_reason"`
		Usage      *anthropicUsage `json:"usage"`
	}
	if json.Unmarshal(raw, &msg) != nil || msg.ID == "" {
		return Result{}, errors.New("invalid provider JSON")
	}
	result := Result{ResponseModel: msg.Model, Status: "success", UsageSource: "unknown"}
	if err := applyAnthropicUsage(&result, msg.Usage); err != nil {
		return Result{}, err
	}
	var content strings.Builder
	calls := make([]any, 0)
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "tool_use":
			if block.ID == "" || block.Name == "" || !json.Valid(block.Input) {
				return Result{}, errors.New("invalid provider tool use")
			}
			args := string(block.Input)
			calls = append(calls, map[string]any{"id": block.ID, "type": "function", "function": map[string]any{"name": block.Name, "arguments": args}})
			fp := newToolStreamFingerprint()
			fp.add(block.Name, args)
			result.ToolCallFingerprints = append(result.ToolCallFingerprints, fp.sum())
		default:
			return Result{}, errors.New("unsupported provider content")
		}
	}
	message := map[string]any{"role": "assistant", "content": content.String()}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	finish := anthropicFinish(msg.StopReason)
	payload := map[string]any{"id": msg.ID, "object": "chat.completion", "model": msg.Model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}}
	if msg.Usage != nil && result.InputTokens != nil && result.OutputTokens != nil {
		if *result.OutputTokens > math.MaxInt64-*result.InputTokens {
			return Result{}, errors.New("usage overflow")
		}
		total := *result.InputTokens + *result.OutputTokens
		usage := map[string]any{"prompt_tokens": *result.InputTokens, "completion_tokens": *result.OutputTokens, "total_tokens": total}
		if result.CachedTokens != nil {
			usage["prompt_tokens_details"] = map[string]any{"cached_tokens": *result.CachedTokens}
		}
		payload["usage"] = usage
	}
	downstream.Header().Set("Content-Type", "application/json")
	downstream.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(downstream).Encode(payload); err != nil {
		return result, err
	}
	return result, nil
}

func anthropicFinish(reason string) any {
	switch reason {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	}
	return nil
}

func writeAnthropicChunk(w http.ResponseWriter, id, model string, delta map[string]any, finish any, usage any) error {
	chunk := map[string]any{"id": id, "object": "chat.completion.chunk", "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
	if usage != nil {
		chunk["usage"] = usage
	}
	raw, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
		return err
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

func (a *Anthropic) Stream(ctx context.Context, resp *http.Response, downstream http.ResponseWriter) (result Result, streamErr error) {
	defer resp.Body.Close()
	downstream.Header().Set("Content-Type", "text/event-stream")
	downstream.Header().Set("Cache-Control", "no-cache")
	downstream.Header().Set("X-Accel-Buffering", "no")
	downstream.WriteHeader(http.StatusOK)
	result = Result{Status: "success", UsageSource: "unknown"}
	defer func() {
		if streamErr == nil || result.Status == "client_cancelled" {
			return
		}
		code := "provider_stream_error"
		if result.Status == "transport_error" {
			code = "provider_stream_interrupted"
		} else {
			result.Status = "provider_error"
		}
		result.ErrorCode = code
		streamError(downstream, code)
	}()
	includeUsage := resp.Header.Get("X-Virgil-Stream-Include-Usage") == "true"
	reader := bufio.NewReader(resp.Body)
	var frame []byte
	id, model := "", ""
	toolIndices := map[int]int{}
	for {
		if err := ctx.Err(); err != nil {
			result.Status = "client_cancelled"
			return result, err
		}
		line, err := readLineBounded(reader)
		if len(frame)+len(line) > maxSSEFrame {
			result.Status = "provider_error"
			return result, errors.New("SSE frame exceeds limit")
		}
		frame = append(frame, line...)
		if len(bytes.TrimSpace(line)) == 0 && len(frame) > 0 {
			var data strings.Builder
			for _, part := range strings.Split(string(frame), "\n") {
				part = strings.TrimSuffix(part, "\r")
				if strings.HasPrefix(part, "data:") {
					data.WriteString(strings.TrimSpace(strings.TrimPrefix(part, "data:")))
				}
			}
			frame = frame[:0]
			if data.Len() > 0 {
				var event struct {
					Type    string `json:"type"`
					Index   int    `json:"index"`
					Message struct {
						ID    string          `json:"id"`
						Model string          `json:"model"`
						Usage *anthropicUsage `json:"usage"`
					} `json:"message"`
					ContentBlock struct {
						Type  string          `json:"type"`
						ID    string          `json:"id"`
						Name  string          `json:"name"`
						Text  string          `json:"text"`
						Input json.RawMessage `json:"input"`
					} `json:"content_block"`
					Delta struct {
						Type        string `json:"type"`
						Text        string `json:"text"`
						PartialJSON string `json:"partial_json"`
						StopReason  string `json:"stop_reason"`
					} `json:"delta"`
					Usage *anthropicUsage `json:"usage"`
				}
				if json.Unmarshal([]byte(data.String()), &event) != nil {
					result.Status = "provider_error"
					return result, errors.New("invalid provider SSE data")
				}
				var delta map[string]any
				var finish any
				var usage any
				switch event.Type {
				case "message_start":
					id = event.Message.ID
					model = event.Message.Model
					result.ResponseModel = model
					if err := applyAnthropicUsage(&result, event.Message.Usage); err != nil {
						return result, err
					}
					delta = map[string]any{"role": "assistant"}
				case "content_block_start":
					if event.ContentBlock.Type == "tool_use" {
						idx := len(toolIndices)
						toolIndices[event.Index] = idx
						initialArgs := ""
						if len(event.ContentBlock.Input) > 0 && string(event.ContentBlock.Input) != "null" {
							initialArgs = string(event.ContentBlock.Input)
						}
						delta = map[string]any{"tool_calls": []any{map[string]any{"index": idx, "id": event.ContentBlock.ID, "type": "function", "function": map[string]any{"name": event.ContentBlock.Name, "arguments": initialArgs}}}}
						if err := result.addToolDelta(0, idx, event.ContentBlock.Name, initialArgs); err != nil {
							return result, err
						}
					} else if event.ContentBlock.Type == "text" && event.ContentBlock.Text != "" {
						delta = map[string]any{"content": event.ContentBlock.Text}
					} else if event.ContentBlock.Type != "text" {
						return result, errors.New("unsupported provider content")
					}
				case "content_block_delta":
					if event.Delta.Type == "text_delta" {
						delta = map[string]any{"content": event.Delta.Text}
					} else if event.Delta.Type == "input_json_delta" {
						idx, ok := toolIndices[event.Index]
						if !ok {
							return result, errors.New("unknown provider tool block")
						}
						delta = map[string]any{"tool_calls": []any{map[string]any{"index": idx, "function": map[string]any{"arguments": event.Delta.PartialJSON}}}}
						if err := result.addToolDelta(0, idx, "", event.Delta.PartialJSON); err != nil {
							return result, err
						}
					} else {
						return result, errors.New("unsupported provider delta")
					}
				case "message_delta":
					if err := applyAnthropicUsage(&result, event.Usage); err != nil {
						return result, err
					}
					if result.InputTokens != nil && result.OutputTokens != nil {
						if *result.OutputTokens > math.MaxInt64-*result.InputTokens {
							result.Status = "provider_error"
							return result, errors.New("usage overflow")
						}
					}
					finish = anthropicFinish(event.Delta.StopReason)
					delta = map[string]any{}
				case "message_stop":
					result.finishToolDeltas()
					if includeUsage && result.InputTokens != nil && result.OutputTokens != nil {
						total := *result.InputTokens + *result.OutputTokens
						usageChunk := map[string]any{"prompt_tokens": *result.InputTokens, "completion_tokens": *result.OutputTokens, "total_tokens": total}
						if result.CachedTokens != nil {
							usageChunk["prompt_tokens_details"] = map[string]any{"cached_tokens": *result.CachedTokens}
						}
						raw, _ := json.Marshal(map[string]any{"id": id, "object": "chat.completion.chunk", "model": model, "choices": []any{}, "usage": usageChunk})
						if _, err := fmt.Fprintf(downstream, "data: %s\n\n", raw); err != nil {
							result.Status = "client_cancelled"
							return result, err
						}
						if f, ok := downstream.(http.Flusher); ok {
							f.Flush()
						}
					}
					if _, err := io.WriteString(downstream, "data: [DONE]\n\n"); err != nil {
						result.Status = "client_cancelled"
						return result, err
					}
					if f, ok := downstream.(http.Flusher); ok {
						f.Flush()
					}
					return result, nil
				case "content_block_stop", "ping":
				case "error":
					result.Status = "provider_error"
					return result, errors.New("provider stream error")
				default:
					result.Status = "provider_error"
					return result, errors.New("unsupported provider event")
				}
				if delta != nil {
					if err := writeAnthropicChunk(downstream, id, model, delta, finish, usage); err != nil {
						result.Status = "client_cancelled"
						return result, err
					}
				}
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				result.Status = "client_cancelled"
				return result, ctx.Err()
			}
			result.Status = "transport_error"
			if errors.Is(err, io.EOF) {
				return result, errors.New("provider stream ended before message_stop")
			}
			return result, fmt.Errorf("provider stream interrupted: %w", err)
		}
	}
}

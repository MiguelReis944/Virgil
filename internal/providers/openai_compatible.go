package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxProviderResponse = 16 << 20

type OpenAICompatible struct {
	baseURL string
	client  *http.Client
}

func NewOpenAICompatible(baseURL string, client *http.Client) *OpenAICompatible {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &OpenAICompatible{baseURL: baseURL, client: &noRedirect}
}

type chatRequest struct {
	Model       string            `json:"model"`
	Messages    []json.RawMessage `json:"messages"`
	Stream      bool              `json:"stream"`
	Tools       json.RawMessage   `json:"tools"`
	ToolChoice  json.RawMessage   `json:"tool_choice"`
	Temperature *float64          `json:"temperature"`
	TopP        *float64          `json:"top_p"`
	MaxTokens   *int64            `json:"max_tokens"`
	Stop        json.RawMessage   `json:"stop"`
}

type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  json.RawMessage `json:"tool_calls"`
	ToolCallID string          `json:"tool_call_id"`
	Name       string          `json:"name"`
}

func decodeStrict(raw []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("extra JSON value")
	}
	return nil
}

func (a *OpenAICompatible) Validate(body json.RawMessage) error {
	var req chatRequest
	if err := decodeStrict(body, &req); err != nil {
		return fmt.Errorf("invalid chat request: %w", err)
	}
	if req.Model == "" || len(req.Messages) == 0 {
		return errors.New("model and messages are required")
	}
	if req.Stream {
		return errors.New("streaming is not available yet")
	}
	for _, raw := range req.Messages {
		var msg chatMessage
		if err := decodeStrict(raw, &msg); err != nil {
			return fmt.Errorf("unsupported message: %w", err)
		}
		switch msg.Role {
		case "system", "developer", "user", "assistant", "tool":
		default:
			return errors.New("unsupported message role")
		}
		if len(msg.Content) != 0 && !bytes.Equal(msg.Content, []byte("null")) {
			var content string
			if err := json.Unmarshal(msg.Content, &content); err != nil {
				return errors.New("only text message content is supported")
			}
		}
	}
	return nil
}

func (a *OpenAICompatible) Build(ctx context.Context, body json.RawMessage, key string) (*http.Request, error) {
	if err := a.Validate(body); err != nil {
		return nil, err
	}
	if key == "" {
		return nil, errors.New("provider key is required")
	}
	base, err := url.Parse(a.baseURL)
	if err != nil || base == nil || base.Host == "" || base.User != nil || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, errors.New("invalid provider base URL")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/chat/completions"
	base.RawQuery = ""
	base.Fragment = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	return req, nil
}

func (a *OpenAICompatible) Do(req *http.Request) (*http.Response, error) {
	return a.client.Do(req)
}

func (a *OpenAICompatible) Translate(resp *http.Response, downstream http.ResponseWriter) (Result, error) {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code := fmt.Sprintf("provider_http_%d", resp.StatusCode)
		downstream.Header().Set("Content-Type", "application/json")
		downstream.WriteHeader(resp.StatusCode)
		_ = json.NewEncoder(downstream).Encode(map[string]any{
			"error": map[string]string{
				"message": "provider request failed",
				"type":    "provider_error",
				"code":    code,
			},
		})
		return Result{ErrorCode: code, Status: "provider_error", UsageSource: "unknown"}, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderResponse+1))
	if err != nil {
		return Result{}, fmt.Errorf("read provider response: %w", err)
	}
	if len(raw) > maxProviderResponse {
		return Result{}, errors.New("provider response exceeds limit")
	}
	var parsed struct {
		Model string `json:"model"`
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Result{}, errors.New("invalid provider JSON")
	}
	result := Result{ResponseModel: parsed.Model, Status: "success", UsageSource: "unknown"}
	if parsed.Usage != nil {
		if parsed.Usage.PromptTokens < 0 || parsed.Usage.CompletionTokens < 0 {
			return Result{}, errors.New("invalid provider usage")
		}
		result.InputTokens = &parsed.Usage.PromptTokens
		result.OutputTokens = &parsed.Usage.CompletionTokens
		result.UsageSource = "provider"
	}
	downstream.Header().Set("Content-Type", "application/json")
	downstream.WriteHeader(resp.StatusCode)
	_, _ = downstream.Write(raw)
	return result, nil
}

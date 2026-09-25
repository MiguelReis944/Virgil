package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildChatRequestPreservesConversationAndTools(t *testing.T) {
	input, _, err := parseResponsesRequest([]byte(`{"model":"fixture","input":[{"role":"developer","content":[{"type":"input_text","text":"be brief"}]},{"role":"user","content":"hello"},{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"q\":1}"},{"type":"function_call_output","call_id":"call_1","output":"found"}],"tools":[{"type":"function","name":"lookup","description":"find","parameters":{"type":"object","properties":{"q":{"type":"integer"}}}}],"tool_choice":{"type":"function","name":"lookup"},"max_output_tokens":100,"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := buildChatRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Messages []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tool_choice"`
		MaxTokens     int `json:"max_tokens"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 4 || got.Messages[0].Role != "system" || got.Messages[0].Content != "be brief" || got.Messages[1].Content != "hello" || got.Messages[2].ToolCalls[0].Function.Arguments != `{"q":1}` || got.Messages[3].ToolCallID != "call_1" || got.Tools[0].Function.Name != "lookup" || got.ToolChoice.Function.Name != "lookup" || got.MaxTokens != 100 || !got.StreamOptions.IncludeUsage {
		t.Fatalf("unexpected translation: %s", raw)
	}
}

func TestBuildChatRequestNormalizesCodexLeadingMessages(t *testing.T) {
	input, _, err := parseResponsesRequest([]byte(`{"model":"fixture","instructions":"system instruction","input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"developer instruction"}]},{"type":"message","role":"user","content":"first"},{"type":"message","role":"user","content":"second"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := buildChatRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Messages []struct{ Role, Content string } `json:"messages"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 2 || got.Messages[0].Role != "system" || got.Messages[0].Content != "system instruction\n\ndeveloper instruction" || got.Messages[1].Role != "user" || got.Messages[1].Content != "first\n\nsecond" {
		t.Fatalf("messages = %+v", got.Messages)
	}
}

func TestBuildChatRequestRejectsUnsupportedContent(t *testing.T) {
	for _, body := range []string{
		`{"model":"fixture","input":[{"role":"user","content":[{"type":"input_image","image_url":"x"}]}]}`,
		`{"model":"fixture","input":"hi","tools":[{"type":"web_search"}]}`,
		`{"model":"fixture","input":"hi","text":{"format":{"type":"json_schema"}}}`,
		`{"model":"fixture","input":[{"role":"user","content":"hi","unexpected":true}]}`,
	} {
		input, _, err := parseResponsesRequest([]byte(body))
		if err != nil {
			continue
		}
		if _, err := buildChatRequest(input); err == nil {
			t.Errorf("accepted unsupported request %s", body)
		}
	}
}

func TestBuildChatRequestAcceptsCodexHintsAndNamespace(t *testing.T) {
	input, _, err := parseResponsesRequest([]byte(`{"model":"fixture","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],"reasoning":{"summary":"auto"},"include":["reasoning.encrypted_content"],"prompt_cache_key":"local-session","text":null,"tools":[{"type":"namespace","name":"multi_agent_v1","description":"agents","tools":[{"name":"send_message","description":"send","parameters":{"type":"object"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := buildChatRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Function.Name != "virgil_ns_multi_agent_v1__send_message" {
		t.Fatalf("namespace lost: %s", raw)
	}
}

func TestBuildChatRequestAcceptsCompletedFunctionCallHistory(t *testing.T) {
	input, _, err := parseResponsesRequest([]byte(`{"model":"fixture","input":[{"type":"function_call","id":"fc_1","status":"completed","call_id":"call_1","name":"multi_agent_v1.send_message","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := buildChatRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "virgil_ns_multi_agent_v1__send_message") {
		t.Fatalf("history lost: %s", raw)
	}
}

func TestTranslateChatResponseRestoresNamespace(t *testing.T) {
	raw, err := translateChatResponse([]byte(`{"id":"chatcmpl_1","model":"fixture","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"virgil_ns_multi_agent_v1__send_message","arguments":"{}"}}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Output []struct {
			Name string `json:"name"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 1 || response.Output[0].Name != "multi_agent_v1.send_message" {
		t.Fatalf("namespace not restored: %s", raw)
	}
}

func TestTranslateChatResponsePreservesToolCallsAndUsage(t *testing.T) {
	raw, err := translateChatResponse([]byte(`{"id":"chatcmpl_1","object":"chat.completion","model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":1}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":7,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":2}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Status string `json:"status"`
		Model  string `json:"model"`
		Output []struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"output"`
		Usage struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "completed" || got.Model != "fixture" || len(got.Output) != 1 || got.Output[0].Type != "function_call" || got.Output[0].CallID != "call_1" || got.Output[0].Name != "lookup" || got.Usage.InputTokens != 7 || got.Usage.OutputTokens != 4 || got.Usage.InputTokensDetails.CachedTokens != 2 {
		t.Fatalf("unexpected response: %s", raw)
	}
}

func TestTranslateChatStreamEmitsCompletedResponse(t *testing.T) {
	stream := "data: {\"id\":\"chatcmpl_1\",\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"he\"}}]}\n\n" +
		"data: {\"id\":\"chatcmpl_1\",\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"llo\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"id\":\"chatcmpl_1\",\"model\":\"fixture\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\n" + "data: [DONE]\n\n"
	w := httptest.NewRecorder()
	raw, err := translateChatStream(context.Background(), strings.NewReader(stream), w)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"response.created", "response.output_item.added", "response.content_part.added", "response.output_text.delta", "response.output_text.done", "response.content_part.done", "response.output_item.done", "response.completed"} {
		if !strings.Contains(w.Body.String(), "event: "+event) {
			t.Fatalf("missing %s in SSE: %s", event, w.Body.String())
		}
	}
	if !strings.Contains(string(raw), "hello") {
		t.Fatalf("bad SSE: %s", w.Body.String())
	}
	if _, err := inspectResponsesPayload(raw); err != nil {
		t.Fatal(err)
	}
}

func TestTranslateChatStreamAggregatesIndexedToolCalls(t *testing.T) {
	stream := "data: {\"id\":\"chatcmpl_2\",\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_\",\"type\":\"function\",\"function\":{\"name\":\"look\",\"arguments\":\"{\\\"q\\\":\"}}]}}]}\n\n" +
		"data: {\"id\":\"chatcmpl_2\",\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"1\",\"function\":{\"name\":\"up\",\"arguments\":\"1}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	w := httptest.NewRecorder()
	raw, err := translateChatStream(context.Background(), strings.NewReader(stream), w)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Output []struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 1 || response.Output[0].CallID != "call_1" || response.Output[0].Name != "lookup" || response.Output[0].Arguments != `{"q":1}` {
		t.Fatalf("tool call lost: %s", raw)
	}
}

func TestTranslateChatStreamRejectsOversizedLine(t *testing.T) {
	w := httptest.NewRecorder()
	if _, err := translateChatStream(context.Background(), strings.NewReader("data: "+strings.Repeat("x", 1<<20)+"\n\n"), w); err == nil {
		t.Fatal("accepted oversized stream")
	}
}

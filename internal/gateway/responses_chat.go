package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func chatObject(raw json.RawMessage, allowed ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, errors.New("expected object")
	}
	known := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		known[key] = true
	}
	for key := range fields {
		if !known[key] {
			return nil, fmt.Errorf("unsupported field %q", key)
		}
	}
	return fields, nil
}

func chatString(fields map[string]json.RawMessage, key string) (string, error) {
	var value string
	if err := json.Unmarshal(fields[key], &value); err != nil {
		return "", fmt.Errorf("invalid %s: %w", key, err)
	}
	return value, nil
}

func responseContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("missing content")
	}
	var simple string
	if json.Unmarshal(raw, &simple) == nil {
		return simple, nil
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", errors.New("unsupported content")
	}
	var text strings.Builder
	for _, part := range parts {
		fields, err := chatObject(part, "type", "text")
		if err != nil {
			return "", err
		}
		kind, err := chatString(fields, "type")
		if err != nil || (kind != "input_text" && kind != "output_text") {
			return "", errors.New("unsupported content part")
		}
		value, err := chatString(fields, "text")
		if err != nil {
			return "", err
		}
		text.WriteString(value)
	}
	return text.String(), nil
}

func buildChatRequest(input responsesRequest) ([]byte, error) {
	if input.Background || input.PreviousResponseID != "" || (input.Store != nil && *input.Store) || len(input.Metadata) > 0 || input.Truncation != "" || input.SafetyIdentifier != "" || input.ServiceTier != "" || input.User != "" {
		return nil, errors.New("unsupported Responses option")
	}
	// Codex asks for encrypted reasoning that this provider never returns. The
	// cache key and automatic summary request are hints, not conversation data.
	if len(input.Include) > 0 && string(input.Include) != "null" {
		var include []string
		if err := json.Unmarshal(input.Include, &include); err != nil || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
			return nil, errors.New("unsupported include")
		}
	}
	if len(input.Reasoning) > 0 && string(input.Reasoning) != "null" {
		fields, err := chatObject(input.Reasoning, "summary")
		if err != nil {
			return nil, err
		}
		summary, err := chatString(fields, "summary")
		if err != nil || summary != "auto" {
			return nil, errors.New("unsupported reasoning")
		}
	}
	if len(input.Text) > 0 && string(input.Text) != "null" {
		return nil, errors.New("unsupported text format")
	}
	if input.Model == "" {
		return nil, errors.New("missing model")
	}
	var messages []map[string]any
	if len(input.Instructions) > 0 {
		instruction, err := responseContent(input.Instructions)
		if err != nil {
			return nil, err
		}
		messages = append(messages, map[string]any{"role": "system", "content": instruction})
	}
	var prompt string
	if json.Unmarshal(input.Input, &prompt) == nil {
		messages = append(messages, map[string]any{"role": "user", "content": prompt})
	} else {
		var items []json.RawMessage
		if err := json.Unmarshal(input.Input, &items); err != nil || len(items) == 0 {
			return nil, errors.New("unsupported input")
		}
		for _, item := range items {
			fields, err := chatObject(item, "type", "role", "content", "call_id", "name", "arguments", "output", "id", "status")
			if err != nil {
				return nil, err
			}
			kind := ""
			if len(fields["type"]) > 0 {
				kind, err = chatString(fields, "type")
				if err != nil {
					return nil, err
				}
			}
			switch kind {
			case "", "message":
				if len(fields["call_id"]) > 0 || len(fields["name"]) > 0 || len(fields["arguments"]) > 0 || len(fields["output"]) > 0 {
					return nil, errors.New("unsupported message field")
				}
				if err := checkCompletedItem(fields); err != nil {
					return nil, err
				}
				role, err := chatString(fields, "role")
				if err != nil {
					return nil, err
				}
				if role != "user" && role != "system" && role != "developer" && role != "assistant" {
					return nil, errors.New("unsupported role")
				}
				content, err := responseContent(fields["content"])
				if err != nil {
					return nil, err
				}
				messages = append(messages, map[string]any{"role": role, "content": content})
			case "function_call":
				if len(fields["role"]) > 0 || len(fields["content"]) > 0 || len(fields["output"]) > 0 {
					return nil, errors.New("unsupported function call field")
				}
				if err := checkCompletedItem(fields); err != nil {
					return nil, err
				}
				id, e1 := chatString(fields, "call_id")
				name, e2 := chatString(fields, "name")
				args, e3 := chatString(fields, "arguments")
				if e1 != nil || e2 != nil || e3 != nil || id == "" || name == "" || !json.Valid([]byte(args)) {
					return nil, errors.New("invalid function call")
				}
				messages = append(messages, map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": id, "type": "function", "function": map[string]any{"name": encodeNamespaceCall(name), "arguments": args}}}})
			case "function_call_output":
				if len(fields["role"]) > 0 || len(fields["content"]) > 0 || len(fields["name"]) > 0 || len(fields["arguments"]) > 0 {
					return nil, errors.New("unsupported function output field")
				}
				if err := checkCompletedItem(fields); err != nil {
					return nil, err
				}
				id, e1 := chatString(fields, "call_id")
				content, e2 := responseContent(fields["output"])
				if e1 != nil || e2 != nil || id == "" {
					return nil, errors.New("invalid function output")
				}
				messages = append(messages, map[string]any{"role": "tool", "tool_call_id": id, "content": content})
			default:
				return nil, errors.New("unsupported input item")
			}
		}
	}
	// Hosted Chat models commonly accept one leading system message and require
	// alternating user/assistant turns. Codex can send leading developer guidance
	// and adjacent user items; fold those without changing their order.
	var normalized []map[string]any
	for _, message := range messages {
		role, _ := message["role"].(string)
		if role == "developer" || role == "system" {
			if len(normalized) > 0 && normalized[len(normalized)-1]["role"] != "system" {
				return nil, errors.New("system guidance after conversation start")
			}
			content, _ := message["content"].(string)
			if len(normalized) == 0 {
				normalized = append(normalized, map[string]any{"role": "system", "content": content})
			} else {
				normalized[0]["content"] = normalized[0]["content"].(string) + "\n\n" + content
			}
			continue
		}
		if role == "user" && len(normalized) > 0 && normalized[len(normalized)-1]["role"] == "user" {
			content, _ := message["content"].(string)
			last := normalized[len(normalized)-1]
			last["content"] = last["content"].(string) + "\n\n" + content
			continue
		}
		normalized = append(normalized, message)
	}
	messages = normalized
	request := map[string]any{"model": input.Model, "messages": messages, "stream": input.Stream}
	if input.Stream {
		request["stream_options"] = map[string]bool{"include_usage": true}
	}
	if input.MaxOutputTokens != nil {
		request["max_tokens"] = *input.MaxOutputTokens
	}
	if input.Temperature != nil {
		request["temperature"] = *input.Temperature
	}
	if input.TopP != nil {
		request["top_p"] = *input.TopP
	}
	if input.ParallelToolCalls != nil {
		request["parallel_tool_calls"] = *input.ParallelToolCalls
	}
	if len(input.Tools) > 0 {
		tools := make([]any, 0, len(input.Tools))
		for _, raw := range input.Tools {
			fields, err := chatObject(raw, "type", "name", "description", "parameters", "strict", "tools")
			if err != nil {
				return nil, err
			}
			kind, err := chatString(fields, "type")
			if err != nil || (kind != "function" && kind != "namespace") {
				return nil, errors.New("unsupported tool")
			}
			name, err := chatString(fields, "name")
			if err != nil || name == "" {
				return nil, errors.New("unnamed tool")
			}
			if kind == "namespace" {
				if strings.Contains(name, "__") || len(fields["parameters"]) > 0 || len(fields["strict"]) > 0 {
					return nil, errors.New("unsupported namespace")
				}
				var nested []json.RawMessage
				if err := json.Unmarshal(fields["tools"], &nested); err != nil || len(nested) == 0 {
					return nil, errors.New("empty namespace")
				}
				for _, member := range nested {
					child, err := chatObject(member, "name", "description", "parameters", "strict", "type")
					if err != nil {
						return nil, err
					}
					childName, err := chatString(child, "name")
					if err != nil || childName == "" {
						return nil, errors.New("unnamed namespace tool")
					}
					if len(child["type"]) > 0 {
						childType, e := chatString(child, "type")
						if e != nil || childType != "function" {
							return nil, errors.New("unsupported namespace member")
						}
					}
					function := map[string]any{"name": encodeNamespaceCall(name + "." + childName)}
					for _, key := range []string{"description", "parameters", "strict"} {
						if len(child[key]) > 0 {
							function[key] = child[key]
						}
					}
					tools = append(tools, map[string]any{"type": "function", "function": function})
				}
				continue
			}
			if len(fields["tools"]) > 0 || strings.HasPrefix(name, "virgil_ns_") {
				return nil, errors.New("unsupported function tool")
			}
			function := map[string]any{"name": name}
			for _, key := range []string{"description", "parameters", "strict"} {
				if len(fields[key]) > 0 {
					function[key] = fields[key]
				}
			}
			tools = append(tools, map[string]any{"type": "function", "function": function})
		}
		request["tools"] = tools
	}
	if len(input.ToolChoice) > 0 {
		var choice string
		if json.Unmarshal(input.ToolChoice, &choice) == nil {
			if choice != "auto" && choice != "none" && choice != "required" {
				return nil, errors.New("unsupported tool choice")
			}
			request["tool_choice"] = choice
		} else {
			fields, err := chatObject(input.ToolChoice, "type", "name")
			if err != nil {
				return nil, err
			}
			kind, e1 := chatString(fields, "type")
			name, e2 := chatString(fields, "name")
			if e1 != nil || e2 != nil || kind != "function" || name == "" {
				return nil, errors.New("unsupported tool choice")
			}
			request["tool_choice"] = map[string]any{"type": "function", "function": map[string]string{"name": encodeNamespaceCall(name)}}
		}
	}
	return json.Marshal(request)
}

func checkCompletedItem(fields map[string]json.RawMessage) error {
	if len(fields["id"]) > 0 {
		id, err := chatString(fields, "id")
		if err != nil || id == "" {
			return errors.New("invalid item id")
		}
	}
	if len(fields["status"]) > 0 {
		status, err := chatString(fields, "status")
		if err != nil || status != "completed" {
			return errors.New("unsupported item status")
		}
	}
	return nil
}

func encodeNamespaceCall(name string) string {
	if namespace, member, ok := strings.Cut(name, "."); ok {
		return "virgil_ns_" + namespace + "__" + member
	}
	return name
}

func decodeNamespaceCall(name string) string {
	if encoded, ok := strings.CutPrefix(name, "virgil_ns_"); ok {
		if namespace, member, ok := strings.Cut(encoded, "__"); ok {
			return namespace + "." + member
		}
	}
	return name
}

type chatReply struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string         `json:"role"`
			Content   *string        `json:"content"`
			ToolCalls []chatToolCall `json:"tool_calls"`
		} `json:"message"`
		Delta struct {
			Role      string         `json:"role"`
			Content   *string        `json:"content"`
			ToolCalls []chatToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int64 `json:"prompt_tokens"`
		CompletionTokens    int64 `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}
type chatToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func completeChatReply(reply chatReply, content string, calls []chatToolCall) ([]byte, error) {
	if reply.Model == "" || reply.ID == "" {
		return nil, errors.New("invalid chat response")
	}
	output := make([]any, 0, 1+len(calls))
	if content != "" {
		output = append(output, map[string]any{"id": "msg_" + reply.ID, "type": "message", "status": "completed", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": content}}})
	}
	for _, call := range calls {
		if call.ID == "" || call.Function.Name == "" || !json.Valid([]byte(call.Function.Arguments)) {
			return nil, errors.New("invalid tool call")
		}
		output = append(output, map[string]any{"type": "function_call", "id": "fc_" + call.ID, "call_id": call.ID, "name": decodeNamespaceCall(call.Function.Name), "arguments": call.Function.Arguments, "status": "completed"})
	}
	response := map[string]any{"id": "resp_" + reply.ID, "object": "response", "created_at": 0, "status": "completed", "model": reply.Model, "output": output, "parallel_tool_calls": true}
	if reply.Usage != nil {
		if reply.Usage.PromptTokens < 0 || reply.Usage.CompletionTokens < 0 {
			return nil, errors.New("invalid usage")
		}
		usage := map[string]any{"input_tokens": reply.Usage.PromptTokens, "output_tokens": reply.Usage.CompletionTokens, "total_tokens": reply.Usage.PromptTokens + reply.Usage.CompletionTokens}
		if reply.Usage.PromptTokensDetails != nil {
			usage["input_tokens_details"] = map[string]int64{"cached_tokens": reply.Usage.PromptTokensDetails.CachedTokens}
		}
		response["usage"] = usage
	}
	return json.Marshal(response)
}

func translateChatResponse(raw []byte) ([]byte, error) {
	var reply chatReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, err
	}
	if len(reply.Choices) != 1 || reply.Choices[0].Index != 0 {
		return nil, errors.New("unsupported chat choices")
	}
	choice := reply.Choices[0]
	if choice.Message.Role != "assistant" {
		return nil, errors.New("unexpected chat role")
	}
	content := ""
	if choice.Message.Content != nil {
		content = *choice.Message.Content
	}
	return completeChatReply(reply, content, choice.Message.ToolCalls)
}

func writeChatEvent(w http.ResponseWriter, kind string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, raw); err != nil {
		return err
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

func translateChatStream(ctx context.Context, body io.Reader, w http.ResponseWriter) ([]byte, error) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	reader := bufio.NewReader(body)
	var frame []byte
	var total int
	var reply chatReply
	var content strings.Builder
	calls := make(map[int]*chatToolCall)
	finished, done, created := false, false, false
	textStarted := false
	for !done {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, err := readResponsesLine(reader)
		if len(line) > 0 {
			total += len(line)
			if total > maxResponsesBody || len(frame)+len(line) > 1<<20 {
				return nil, errors.New("chat stream exceeds limit")
			}
			frame = append(frame, line...)
			if len(bytes.TrimSpace(line)) == 0 {
				for _, part := range bytes.Split(frame, []byte("\n")) {
					part = bytes.TrimSpace(part)
					if !bytes.HasPrefix(part, []byte("data:")) {
						continue
					}
					data := bytes.TrimSpace(part[5:])
					if bytes.Equal(data, []byte("[DONE]")) {
						done = true
						break
					}
					var chunk chatReply
					if err := json.Unmarshal(data, &chunk); err != nil {
						return nil, err
					}
					if chunk.ID == "" || chunk.Model == "" {
						return nil, errors.New("incomplete chat chunk")
					}
					if reply.ID != "" && (reply.ID != chunk.ID || reply.Model != chunk.Model) {
						return nil, errors.New("chat stream identity changed")
					}
					reply.ID, reply.Model = chunk.ID, chunk.Model
					if !created {
						if err := writeChatEvent(w, "response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_" + reply.ID, "object": "response", "status": "in_progress", "model": reply.Model, "output": []any{}}}); err != nil {
							return nil, err
						}
						created = true
					}
					if chunk.Usage != nil {
						reply.Usage = chunk.Usage
					}
					for _, choice := range chunk.Choices {
						if choice.Index != 0 {
							return nil, errors.New("unsupported chat choice index")
						}
						if choice.Delta.Content != nil {
							if !textStarted {
								item := map[string]any{"id": "msg_" + reply.ID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}
								if err := writeChatEvent(w, "response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item}); err != nil {
									return nil, err
								}
								if err := writeChatEvent(w, "response.content_part.added", map[string]any{"type": "response.content_part.added", "output_index": 0, "content_index": 0, "part": map[string]string{"type": "output_text", "text": ""}}); err != nil {
									return nil, err
								}
								textStarted = true
							}
							content.WriteString(*choice.Delta.Content)
							if err := writeChatEvent(w, "response.output_text.delta", map[string]any{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "delta": *choice.Delta.Content}); err != nil {
								return nil, err
							}
						}
						for _, fragment := range choice.Delta.ToolCalls {
							call := calls[fragment.Index]
							if call == nil {
								call = &chatToolCall{Index: fragment.Index}
								calls[fragment.Index] = call
							}
							call.ID += fragment.ID
							call.Function.Name += fragment.Function.Name
							call.Function.Arguments += fragment.Function.Arguments
						}
						if choice.FinishReason != nil {
							if *choice.FinishReason != "stop" && *choice.FinishReason != "tool_calls" {
								return nil, errors.New("unsupported finish reason")
							}
							finished = true
						}
					}
				}
				frame = frame[:0]
			}
		}
		if err != nil && !done {
			return nil, err
		}
	}
	if !finished {
		return nil, errors.New("chat stream not finished")
	}
	ordered := make([]chatToolCall, 0, len(calls))
	for i := 0; i < len(calls); i++ {
		call := calls[i]
		if call == nil {
			return nil, errors.New("non-contiguous tool calls")
		}
		ordered = append(ordered, *call)
	}
	raw, err := completeChatReply(reply, content.String(), ordered)
	if err != nil {
		return nil, err
	}
	if textStarted {
		if err := writeChatEvent(w, "response.output_text.done", map[string]any{"type": "response.output_text.done", "output_index": 0, "content_index": 0, "text": content.String()}); err != nil {
			return nil, err
		}
		part := map[string]string{"type": "output_text", "text": content.String()}
		if err := writeChatEvent(w, "response.content_part.done", map[string]any{"type": "response.content_part.done", "output_index": 0, "content_index": 0, "part": part}); err != nil {
			return nil, err
		}
		item := map[string]any{"id": "msg_" + reply.ID, "type": "message", "status": "completed", "role": "assistant", "content": []any{part}}
		if err := writeChatEvent(w, "response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}); err != nil {
			return nil, err
		}
	}
	for i, call := range ordered {
		index := i
		if textStarted {
			index++
		}
		item := map[string]any{"type": "function_call", "id": "fc_" + call.ID, "call_id": call.ID, "name": decodeNamespaceCall(call.Function.Name), "arguments": call.Function.Arguments, "status": "completed"}
		if err := writeChatEvent(w, "response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": index, "item": item}); err != nil {
			return nil, err
		}
		if err := writeChatEvent(w, "response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": index, "item": item}); err != nil {
			return nil, err
		}
	}
	var response any
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if err := writeChatEvent(w, "response.completed", map[string]any{"type": "response.completed", "response": response}); err != nil {
		return nil, err
	}
	return raw, nil
}

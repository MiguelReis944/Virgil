package providers

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

const maxSSEFrame = 1 << 20

func readLineBounded(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > maxSSEFrame {
			return nil, errors.New("SSE line exceeds limit")
		}
		line = append(line, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}

func streamError(w http.ResponseWriter, code string) {
	_, _ = io.WriteString(w, "event: error\ndata: {\"error\":{\"type\":\"provider_error\",\"code\":\""+code+"\",\"message\":\"provider stream failed\"}}\n\n")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (a *OpenAICompatible) Stream(ctx context.Context, resp *http.Response, downstream http.ResponseWriter) (Result, error) {
	defer resp.Body.Close()
	downstream.Header().Set("Content-Type", "text/event-stream")
	downstream.Header().Set("Cache-Control", "no-cache")
	downstream.Header().Set("X-Accel-Buffering", "no")
	downstream.WriteHeader(http.StatusOK)
	reader := bufio.NewReader(resp.Body)
	result := Result{Status: "success", UsageSource: "unknown"}
	var frame []byte
	for {
		if err := ctx.Err(); err != nil {
			result.Status = "client_cancelled"
			return result, err
		}
		line, err := readLineBounded(reader)
		if len(line) > 0 {
			if len(frame)+len(line) > maxSSEFrame {
				streamError(downstream, "stream_frame_too_large")
				result.Status = "provider_error"
				return result, errors.New("SSE frame exceeds limit")
			}
			frame = append(frame, line...)
			if len(bytes.TrimSpace(line)) == 0 {
				done, parseErr := inspectSSEFrame(frame, &result)
				if parseErr != nil {
					streamError(downstream, "provider_stream_error")
					result.Status = "provider_error"
					return result, parseErr
				}
				if _, writeErr := downstream.Write(frame); writeErr != nil {
					result.Status = "client_cancelled"
					return result, writeErr
				}
				if flusher, ok := downstream.(http.Flusher); ok {
					flusher.Flush()
				}
				frame = frame[:0]
				if done {
					result.ResponseText = result.contentBuf.String()
					return result, nil
				}
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				result.Status = "client_cancelled"
				return result, ctx.Err()
			}
			streamError(downstream, "provider_stream_interrupted")
			result.Status = "transport_error"
			if errors.Is(err, io.EOF) {
				return result, errors.New("provider stream ended before DONE")
			}
			return result, fmt.Errorf("provider stream interrupted: %w", err)
		}
	}
}

func inspectSSEFrame(frame []byte, result *Result) (bool, error) {
	var data strings.Builder
	providerError := false
	for _, line := range strings.Split(string(frame), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "event: error" {
			providerError = true
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	payload := data.String()
	if providerError {
		return false, errors.New("provider stream error event")
	}
	if payload == "[DONE]" {
		result.finishToolDeltas()
		return true, nil
	}
	if payload == "" {
		return false, nil
	}
	var chunk struct {
		Model   string          `json:"model"`
		Error   json.RawMessage `json:"error"`
		Usage   *openaiUsage    `json:"usage"`
		Choices []struct {
			Index int `json:"index"`
			Delta struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int `json:"index"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return false, errors.New("invalid provider SSE data")
	}
	if len(chunk.Error) != 0 && string(chunk.Error) != "null" {
		return false, errors.New("provider stream error payload")
	}
	if chunk.Model != "" {
		result.ResponseModel = chunk.Model
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			result.contentBuf.WriteString(choice.Delta.Content)
		}
		for _, call := range choice.Delta.ToolCalls {
			if err := result.addToolDelta(choice.Index, call.Index, call.Function.Name, call.Function.Arguments); err != nil {
				return false, err
			}
		}
	}
	if err := applyUsage(result, chunk.Usage); err != nil {
		return false, err
	}
	return false, nil
}

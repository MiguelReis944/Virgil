package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/MiguelReis944/Virgil/internal/policies"
)

const maxToolResultRequest = 4096

func (router *router) toolResults(w http.ResponseWriter, req *http.Request) {
	token := router.getenv("VIRGIL_LOCAL_APP_TOKEN")
	if token == "" || router.policy == nil {
		http.NotFound(w, req)
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Header.Get("X-Virgil-App-Token")), []byte(token)) != 1 {
		writeAPIError(w, http.StatusUnauthorized, "invalid_app_token")
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxToolResultRequest)
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		RunID      string `json:"run_id"`
		ToolCallID string `json:"tool_call_id"`
		ToolName   string `json:"tool_name"`
		Status     string `json:"status"`
		ErrorCode  string `json:"error_code"`
	}
	if err := decoder.Decode(&input); err != nil {
		writeToolResultDecodeError(w, err)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeToolResultDecodeError(w, err)
		return
	}
	_, err := router.policy.RecordToolResult(req.Context(), policies.ToolResult{
		RunID: input.RunID, ToolCallID: input.ToolCallID,
		ToolName: input.ToolName, Status: input.Status, ErrorCode: input.ErrorCode,
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_tool_result")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeToolResultDecodeError(w http.ResponseWriter, err error) {
	var sizeErr *http.MaxBytesError
	if errors.As(err, &sizeErr) {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large")
		return
	}
	writeAPIError(w, http.StatusBadRequest, "invalid_tool_result")
}

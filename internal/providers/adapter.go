package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type Result struct {
	ResponseModel        string
	InputTokens          *int64
	OutputTokens         *int64
	CachedTokens         *int64
	UsageSource          string
	ErrorCode            string
	Status               string
	ToolCallFingerprints []string
	toolStreams          map[toolStreamKey]*toolStreamFingerprint
	// Content fields — populated by adapters that support capture; empty otherwise.
	ResponseText string // assistant message text on success
	ErrorBody    string // provider error body on failure
	contentBuf   strings.Builder
}

type Adapter interface {
	Validate(body json.RawMessage) error
	Build(ctx context.Context, body json.RawMessage, key string) (*http.Request, error)
	Do(req *http.Request) (*http.Response, error)
	Translate(resp *http.Response, downstream http.ResponseWriter) (Result, error)
	Stream(ctx context.Context, resp *http.Response, downstream http.ResponseWriter) (Result, error)
}

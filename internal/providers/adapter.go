package providers

import (
	"context"
	"encoding/json"
	"net/http"
)

type Result struct {
	ResponseModel string
	InputTokens   *int64
	OutputTokens  *int64
	CachedTokens  *int64
	UsageSource   string
	ErrorCode     string
	Status        string
}

type Adapter interface {
	Validate(body json.RawMessage) error
	Build(ctx context.Context, body json.RawMessage, key string) (*http.Request, error)
	Do(req *http.Request) (*http.Response, error)
	Translate(resp *http.Response, downstream http.ResponseWriter) (Result, error)
}

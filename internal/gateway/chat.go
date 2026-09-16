package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/providers"
)

const maxChatRequest = 1 << 20

type route struct {
	adapter  providers.Adapter
	keyEnv   string
	provider string
}

type router struct {
	models map[string]route
	getenv func(string) string
}

func newRouter(cfg config.Config, client *http.Client, getenv func(string) string) (*router, error) {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	r := &router{models: make(map[string]route), getenv: getenv}
	for name, provider := range cfg.Providers {
		if provider.Type != "openai-compatible" && provider.Type != "openai" {
			return nil, errors.New("unsupported provider type")
		}
		if provider.Model == "" || provider.BaseURL == "" {
			return nil, errors.New("provider model and base_url are required")
		}
		if _, exists := r.models[provider.Model]; exists {
			return nil, errors.New("duplicate configured model")
		}
		r.models[provider.Model] = route{
			adapter:  providers.NewOpenAICompatible(provider.BaseURL, client),
			keyEnv:   provider.APIKeyEnv,
			provider: name,
		}
	}
	return r, nil
}

func writeAPIError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"message": http.StatusText(status),
			"type":    "gateway_error",
			"code":    code,
		},
	})
}

func (router *router) chat(w http.ResponseWriter, req *http.Request) {
	req.Body = http.MaxBytesReader(w, req.Body, maxChatRequest)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large")
		return
	}
	var selector struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &selector); err != nil || selector.Model == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	selected, ok := router.models[selector.Model]
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "model_not_configured")
		return
	}
	if err := selected.adapter.Validate(body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "unsupported_request")
		return
	}
	key, ok := router.resolveKey(req.Header.Get("Authorization"), selected.keyEnv)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "provider_key_required")
		return
	}
	upstreamReq, err := selected.adapter.Build(req.Context(), body, key)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_provider_config")
		return
	}
	resp, err := selected.adapter.Do(upstreamReq)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "provider_transport_error")
		return
	}
	if selector.Stream && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = selected.adapter.Stream(req.Context(), resp, w)
		return
	}
	if _, err := selected.adapter.Translate(resp, w); err != nil {
		writeAPIError(w, http.StatusBadGateway, "provider_response_error")
	}
}

func (router *router) resolveKey(header, providerKeyEnv string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	presented := parts[1]
	localToken := router.getenv("VIRGIL_LOCAL_APP_TOKEN")
	if localToken != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(localToken)) == 1 {
		if providerKeyEnv == "" {
			return "", false
		}
		providerKey := router.getenv(providerKeyEnv)
		return providerKey, providerKey != ""
	}
	return presented, true
}

package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

// ExecutionAuthenticator resolves an active run credential to its server-bound run ID.
type ExecutionAuthenticator interface {
	ResolveRunToken(string) (runID string, ok bool)
}

type requestIdentity struct {
	RunID            string
	UseConfiguredKey bool
	Supervised       bool
}

type executionIdentityKey struct{}

func identityFromRequest(req *http.Request) (requestIdentity, bool) {
	identity, ok := req.Context().Value(executionIdentityKey{}).(requestIdentity)
	return identity, ok && identity.Supervised
}

func (router *router) authenticateExecution(next http.HandlerFunc, tokenHeader string) http.HandlerFunc {
	if router.executionAuth == nil && router.getenv("VIRGIL_DESKTOP_TOKEN") == "" {
		return next
	}
	return func(w http.ResponseWriter, req *http.Request) {
		token := req.Header.Get(tokenHeader)
		if tokenHeader == "Authorization" {
			parts := strings.Fields(token)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				writeAPIError(w, http.StatusUnauthorized, "invalid_execution_token")
				return
			}
			token = parts[1]
		}
		if router.executionAuth != nil {
			if runID, ok := router.executionAuth.ResolveRunToken(token); ok && runID != "" {
				identity := requestIdentity{RunID: runID, UseConfiguredKey: true, Supervised: true}
				req = req.WithContext(context.WithValue(req.Context(), executionIdentityKey{}, identity))
				req.Header.Set("X-Virgil-Run-ID", runID)
				next(w, req)
				return
			}
		}
		identity, ok := router.desktopIdentity(req, token)
		if !ok {
			writeAPIError(w, http.StatusUnauthorized, "invalid_execution_token")
			return
		}
		req = req.WithContext(context.WithValue(req.Context(), executionIdentityKey{}, identity))
		req.Header.Set("X-Virgil-Run-ID", identity.RunID)
		next(w, req)
	}
}

func (router *router) desktopIdentity(req *http.Request, token string) (requestIdentity, bool) {
	secret := router.getenv("VIRGIL_DESKTOP_TOKEN")
	if len(secret) < 32 || token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(secret)) != 1 {
		return requestIdentity{}, false
	}
	kind := "codex"
	if req.URL.Path == "/v1/messages" {
		kind = "claude"
	}
	return requestIdentity{RunID: desktopSessionRunID(kind, req.Header.Get("x-claude-code-session-id")), UseConfiguredKey: true}, true
}

// desktopSessionRunID maps an untrusted client session identifier to an opaque,
// bounded policy key. Invalid or missing identifiers use a stable client bucket.
func desktopSessionRunID(kind, session string) string {
	base := "desktop_" + kind
	if len(session) == 0 || len(session) > 256 {
		return base
	}
	for _, ch := range session {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == '.') {
			return base
		}
	}
	sum := sha256.Sum256([]byte(session))
	return base + "_" + hex.EncodeToString(sum[:12])
}

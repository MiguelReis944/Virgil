package gateway

import (
	"context"
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
}

type executionIdentityKey struct{}

func identityFromRequest(req *http.Request) (requestIdentity, bool) {
	identity, ok := req.Context().Value(executionIdentityKey{}).(requestIdentity)
	return identity, ok
}

func (router *router) authenticateExecution(next http.HandlerFunc, tokenHeader string) http.HandlerFunc {
	if router.executionAuth == nil {
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
		runID, ok := router.executionAuth.ResolveRunToken(token)
		if !ok || runID == "" {
			writeAPIError(w, http.StatusUnauthorized, "invalid_execution_token")
			return
		}
		identity := requestIdentity{RunID: runID, UseConfiguredKey: true}
		req = req.WithContext(context.WithValue(req.Context(), executionIdentityKey{}, identity))
		req.Header.Set("X-Virgil-Run-ID", runID)
		next(w, req)
	}
}

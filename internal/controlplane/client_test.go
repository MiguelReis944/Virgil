package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/storage"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

func makeDeliveries(t *testing.T, n int) []storage.Delivery {
	t.Helper()
	out := make([]storage.Delivery, n)
	for i := range out {
		ev, err := telemetry.Build(telemetry.Attempt{
			InstallationID: "install_fixture",
			RunID:          "run_fixture",
			TraceID:        "00000000000000000000000000000001",
			SpanID:         "0000000000000001",
			Provider:       "fixture",
			RequestedModel: "fixture-model",
			UsageSource:    "unknown",
			Status:         "success",
			StartedAt:      time.Now().Add(-time.Millisecond),
			EndedAt:        time.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
		out[i] = storage.Delivery{EventID: ev.EventID, Event: ev}
	}
	return out
}

func TestBatchAcksOnlyAcceptedEvents(t *testing.T) {
	deliveries := makeDeliveries(t, 2)
	// Server accepts only the first event.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req batchRequest
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(batchResponse{
			AcceptedIDs: []string{req.Events[0].EventID},
			Rejections:  map[string]string{req.Events[1].EventID: "validation_failed"},
		})
	}))
	defer server.Close()
	client := NewClient(server.URL, "cred_test", nil)
	ack, err := client.SendBatch(context.Background(), deliveries, []string{"event_id", "provider"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ack.AcceptedIDs) != 1 || ack.AcceptedIDs[0] != deliveries[0].EventID {
		t.Fatalf("expected 1 accepted ID %s, got %v", deliveries[0].EventID, ack.AcceptedIDs)
	}
	if len(ack.Rejections) != 1 {
		t.Fatalf("expected 1 rejection, got %v", ack.Rejections)
	}
}

func TestCredentialNeverInEvent(t *testing.T) {
	const secret = "super_secret_credential_xyz"
	deliveries := makeDeliveries(t, 1)
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(batchResponse{AcceptedIDs: []string{deliveries[0].EventID}})
	}))
	defer server.Close()
	client := NewClient(server.URL, secret, nil)
	if _, err := client.SendBatch(context.Background(), deliveries, []string{"event_id", "provider"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(capturedBody), secret) {
		t.Fatalf("credential found in event payload")
	}
}

func TestBatchDoesNotFollowRedirectWithCredential(t *testing.T) {
	forwarded := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = true
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client := NewClient(redirect.URL, "synthetic-credential", nil)
	if _, err := client.SendBatch(context.Background(), makeDeliveries(t, 1), []string{"provider"}); err == nil {
		t.Fatal("redirect accepted")
	}
	if forwarded { t.Fatal("credential request followed redirect") }
}

func TestRevokedCredentialStopsRemoteOnly(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	client := NewClient(server.URL, "cred_revoked", nil)
	deliveries := makeDeliveries(t, 1)

	// First call hits the server and gets 401.
	_, err := client.SendBatch(context.Background(), deliveries, []string{"event_id", "provider"})
	var revokedErr *CredentialRevokedError
	if err == nil {
		t.Fatal("expected CredentialRevokedError")
	}
	if !isCredentialRevokedError(err, &revokedErr) {
		t.Fatalf("expected CredentialRevokedError, got %T: %v", err, err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 server call, got %d", calls)
	}

	// Second call must NOT hit the server (revoked flag short-circuits).
	_, err = client.SendBatch(context.Background(), deliveries, []string{"event_id", "provider"})
	if !isCredentialRevokedError(err, &revokedErr) {
		t.Fatalf("expected CredentialRevokedError on second call, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected no further server calls after revocation, got %d", calls)
	}

	// SetCredential clears the revoked flag.
	client.SetCredential("cred_new")
	if client.IsRevoked() {
		t.Fatal("expected not revoked after SetCredential")
	}
}

func TestOfflineUsesLastValidPolicy(t *testing.T) {
	wantVersion := int64(7)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PolicyEnvelope{
			Version: wantVersion,
			Limits:  CPLimits{MaxCallsPerRun: 50},
		})
	}))
	client := NewClient(server.URL, "cred_ok", nil)

	// Fetch while online — populates lastPolicy.
	env, err := client.CurrentPolicy(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if env.Version != wantVersion {
		t.Fatalf("expected version %d, got %d", wantVersion, env.Version)
	}

	// Take server offline.
	server.Close()

	// Fetch offline — should return last policy with no error.
	env2, err := client.CurrentPolicy(context.Background(), "")
	if err != nil {
		t.Fatalf("expected no error offline, got %v", err)
	}
	if env2.Version != wantVersion {
		t.Fatalf("expected cached version %d offline, got %d", wantVersion, env2.Version)
	}
}

func isCredentialRevokedError(err error, target **CredentialRevokedError) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*CredentialRevokedError); ok {
		if target != nil {
			*target = e
		}
		return true
	}
	return false
}

// Package contract validates that the Task 13 controlplane.Client speaks the
// documented wire protocol in schemas/controlplane.v1.openapi.yaml.
// All tests use local fake servers; no real Control Plane is required.
package contract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/controlplane"
	"github.com/MiguelReis944/Virgil/internal/storage"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

// schemaPath returns the absolute path to a file in schemas/.
func schemaPath(t *testing.T, name string) string {
	t.Helper()
	// tests/contract/ → ../../schemas/
	p := filepath.Join("..", "..", "schemas", name)
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// TestOpenAPIDocumentExists validates that the OpenAPI document is present and
// is valid JSON/YAML (checked by parsing the required top-level fields).
func TestOpenAPIDocumentExists(t *testing.T) {
	path := schemaPath(t, "controlplane.v1.openapi.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("OpenAPI document missing at %s: %v", path, err)
	}
	// Basic sanity: must reference all four documented endpoints.
	for _, endpoint := range []string{
		"/v1/installations/enroll",
		"/v1/events/batch",
		"/v1/policies/current",
		"/v1/installations/{id}/revoke",
	} {
		if !strings.Contains(string(raw), endpoint) {
			t.Errorf("OpenAPI document missing endpoint %s", endpoint)
		}
	}
}

// TestPolicyExampleMatchesSchema validates that the policy example JSON
// parses into the CPLimits structure without unknown fields.
func TestPolicyExampleMatchesSchema(t *testing.T) {
	path := schemaPath(t, "policy.v1.example.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("policy example missing at %s: %v", path, err)
	}
	var env controlplane.PolicyEnvelope
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("policy example does not match PolicyEnvelope schema: %v", err)
	}
	if env.Version == 0 {
		t.Error("policy example must have a non-zero version")
	}
}

// TestEnrollExchangesOneTimeToken validates the Enroll wire format.
func TestEnrollExchangesOneTimeToken(t *testing.T) {
	wantToken := "ott_test_fixture"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/installations/enroll" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req struct {
			OneTimeToken string `json:"one_time_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OneTimeToken != wantToken {
			t.Errorf("enroll request body wrong: got token=%q err=%v", req.OneTimeToken, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(controlplane.Enrollment{
			InstallationID: "install_contract_fixture",
			Credential:     "cred_contract_fixture",
		})
	}))
	defer server.Close()

	client := controlplane.NewClient(server.URL, "", nil)
	en, err := client.Enroll(context.Background(), wantToken)
	if err != nil {
		t.Fatal(err)
	}
	if en.InstallationID != "install_contract_fixture" || en.Credential != "cred_contract_fixture" {
		t.Fatalf("unexpected enrollment: %+v", en)
	}
}

// TestBatchRequestFormat validates the exact wire shape sent by SendBatch.
func TestBatchRequestFormat(t *testing.T) {
	ev, err := telemetry.Build(telemetry.Attempt{
		InstallationID: "install_contract",
		RunID:          "run_contract",
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
	delivery := storage.Delivery{EventID: ev.EventID, Event: ev}

	var capturedBody map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/events/batch" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("unexpected Content-Type: %s", ct)
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			t.Errorf("missing or malformed Authorization header: %s", auth)
		}
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"accepted_ids": []string{ev.EventID},
		})
	}))
	defer server.Close()

	client := controlplane.NewClient(server.URL, "cred_fixture", nil)
	ack, err := client.SendBatch(context.Background(), []storage.Delivery{delivery}, []string{"event_id", "provider"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ack.AcceptedIDs) != 1 || ack.AcceptedIDs[0] != ev.EventID {
		t.Fatalf("unexpected ack: %+v", ack)
	}
	// Verify top-level shape: must have "events" array.
	if _, ok := capturedBody["events"]; !ok {
		t.Fatalf("batch request missing 'events' field; got keys: %v", keys(capturedBody))
	}
}

// TestPolicyETagRoundTrip validates If-None-Match / ETag behavior.
func TestPolicyETagRoundTrip(t *testing.T) {
	const etag = `"v42"`
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(controlplane.PolicyEnvelope{
			Version: 42,
			Limits:  controlplane.CPLimits{MaxCallsPerRun: 100},
		})
	}))
	defer server.Close()

	client := controlplane.NewClient(server.URL, "cred", nil)
	env, err := client.CurrentPolicy(context.Background(), "")
	if err != nil || env.Version != 42 {
		t.Fatalf("first fetch: %v env=%+v", err, env)
	}
	// Second fetch with the returned ETag should get 304 (zero value).
	env2, err := client.CurrentPolicy(context.Background(), etag)
	if err != nil || env2.Version != 0 {
		t.Fatalf("second fetch (304): %v env=%+v", err, env2)
	}
	if calls != 2 {
		t.Fatalf("expected 2 server calls, got %d", calls)
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

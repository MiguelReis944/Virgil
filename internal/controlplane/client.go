package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/MiguelReis944/Virgil/internal/storage"
)

// Enrollment is returned by a successful Enroll call.
type Enrollment struct {
	InstallationID string `json:"installation_id"`
	Credential     string `json:"credential"`
}

// BatchAck is returned by a successful SendBatch call.
type BatchAck struct {
	AcceptedIDs []string          `json:"accepted_ids"`
	Rejections  map[string]string `json:"rejections"`
}

// CredentialRevokedError is returned when the server signals the credential is invalid.
type CredentialRevokedError struct {
	StatusCode int
}

func (e *CredentialRevokedError) Error() string {
	return fmt.Sprintf("control plane credential revoked (HTTP %d)", e.StatusCode)
}

// Client communicates with a Virgil Control Plane.
// After receiving a 401 or 403 the client marks itself revoked and stops
// making network requests for batch and policy endpoints.
type Client struct {
	mu         sync.Mutex
	endpoint   string
	credential string
	httpClient *http.Client
	revoked    bool
	lastPolicy PolicyEnvelope
}

// NewClient creates a new Client. credential may be empty before Enroll.
func NewClient(endpoint, credential string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{endpoint: endpoint, credential: credential, httpClient: httpClient}
}

// SetCredential updates the credential (e.g., after Enroll).
func (c *Client) SetCredential(credential string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.credential = credential
	c.revoked = false
}

// Credential returns the current credential.
func (c *Client) Credential() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.credential
}

// IsRevoked reports whether the credential has been revoked by the server.
func (c *Client) IsRevoked() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.revoked
}

// LastPolicy returns the most recently successful policy response.
// Returns the zero value if no policy has been fetched yet.
func (c *Client) LastPolicy() PolicyEnvelope {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastPolicy
}

func (c *Client) markRevoked() {
	c.mu.Lock()
	c.revoked = true
	c.mu.Unlock()
}

// Enroll exchanges a one-time token for an installation credential.
func (c *Client) Enroll(ctx context.Context, oneTimeToken string) (Enrollment, error) {
	body, err := json.Marshal(map[string]string{"one_time_token": oneTimeToken})
	if err != nil {
		return Enrollment{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/installations/enroll", bytes.NewReader(body))
	if err != nil {
		return Enrollment{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Enrollment{}, fmt.Errorf("enroll: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return Enrollment{}, fmt.Errorf("enroll: server returned %d", resp.StatusCode)
	}
	var en Enrollment
	if err := json.NewDecoder(resp.Body).Decode(&en); err != nil {
		return Enrollment{}, fmt.Errorf("enroll: decode: %w", err)
	}
	if en.Credential == "" {
		return Enrollment{}, errors.New("enroll: empty credential in response")
	}
	return en, nil
}

type batchRequest struct {
	Events []batchEvent `json:"events"`
}

type batchEvent struct {
	EventID string          `json:"event_id"`
	Payload json.RawMessage `json:"payload"`
}

type batchResponse struct {
	AcceptedIDs []string          `json:"accepted_ids"`
	Rejections  map[string]string `json:"rejections"`
}

// SendBatch posts a batch of events to the Control Plane.
// Returns CredentialRevokedError on 401/403 and stops future remote calls.
func (c *Client) SendBatch(ctx context.Context, batch []storage.Delivery) (BatchAck, error) {
	if c.IsRevoked() {
		return BatchAck{}, &CredentialRevokedError{StatusCode: http.StatusUnauthorized}
	}
	events := make([]batchEvent, 0, len(batch))
	for _, d := range batch {
		payload, err := json.Marshal(d.Event)
		if err != nil {
			return BatchAck{}, fmt.Errorf("marshal event %s: %w", d.EventID, err)
		}
		events = append(events, batchEvent{EventID: d.EventID, Payload: payload})
	}
	body, err := json.Marshal(batchRequest{Events: events})
	if err != nil {
		return BatchAck{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/events/batch", bytes.NewReader(body))
	if err != nil {
		return BatchAck{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	cred := c.Credential()
	if cred != "" {
		req.Header.Set("Authorization", "Bearer "+cred)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return BatchAck{}, fmt.Errorf("send batch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		c.markRevoked()
		return BatchAck{}, &CredentialRevokedError{StatusCode: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return BatchAck{}, fmt.Errorf("send batch: server returned %d", resp.StatusCode)
	}
	var ack batchResponse
	if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil {
		return BatchAck{}, fmt.Errorf("send batch: decode: %w", err)
	}
	return BatchAck{AcceptedIDs: ack.AcceptedIDs, Rejections: ack.Rejections}, nil
}

// CurrentPolicy fetches the current policy envelope.
// Pass the last known ETag to avoid redundant transfers (server returns 304).
// Returns the zero PolicyEnvelope on 304 (not modified).
// Returns CredentialRevokedError on 401/403 and stops future remote calls.
// On transport error, returns the last successfully fetched policy.
func (c *Client) CurrentPolicy(ctx context.Context, etag string) (PolicyEnvelope, error) {
	if c.IsRevoked() {
		return PolicyEnvelope{}, &CredentialRevokedError{StatusCode: http.StatusUnauthorized}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/v1/policies/current", nil)
	if err != nil {
		return PolicyEnvelope{}, err
	}
	cred := c.Credential()
	if cred != "" {
		req.Header.Set("Authorization", "Bearer "+cred)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Offline: return last valid policy without error so callers can continue.
		return c.LastPolicy(), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		c.markRevoked()
		return PolicyEnvelope{}, &CredentialRevokedError{StatusCode: resp.StatusCode}
	}
	if resp.StatusCode == http.StatusNotModified {
		return PolicyEnvelope{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return PolicyEnvelope{}, fmt.Errorf("current policy: server returned %d", resp.StatusCode)
	}
	var env PolicyEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return PolicyEnvelope{}, fmt.Errorf("current policy: decode: %w", err)
	}
	env.ETag = resp.Header.Get("ETag")
	c.mu.Lock()
	c.lastPolicy = env
	c.mu.Unlock()
	return env, nil
}

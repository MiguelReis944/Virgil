package export

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/MiguelReis944/Virgil/internal/storage"
)

// Ack holds the result of an OTLP send operation.
type Ack struct {
	AcceptedIDs []string
}

// OTLPSender sends batches to an OTLP/HTTP trace endpoint.
type OTLPSender struct {
	endpoint string // e.g. "http://collector:4318"
	client   *http.Client
	headers  map[string]string
}

func NewOTLPSender(endpoint string, headers map[string]string, client *http.Client) *OTLPSender {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &OTLPSender{endpoint: endpoint, headers: headers, client: client}
}

// Send encodes deliveries as OTLP trace protobuf and posts to /v1/traces.
// Returns acked event IDs (all on success) and an error on failure.
// Callers should use exponential backoff for retryable errors.
func (s *OTLPSender) Send(ctx context.Context, batch []storage.Delivery, allowed []string) ([]string, error) {
	if len(batch) == 0 {
		return nil, nil
	}
	if len(allowed) == 0 {
		return nil, errors.New("export field allowlist is required")
	}
	resourceSpans := deliveriesToResourceSpans(batch, allowed)
	// Manually encode ExportTraceServiceRequest (field 1 = repeated ResourceSpans)
	// to avoid importing the collector service package (which brings in grpc).
	body, err := encodeExportRequest(resourceSpans)
	if err != nil {
		return nil, fmt.Errorf("marshal OTLP: %w", err)
	}
	url := s.endpoint + "/v1/traces"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	for k, v := range s.headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("otlp send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		ids := make([]string, len(batch))
		for i, d := range batch {
			ids[i] = d.EventID
		}
		return ids, nil
	}
	return nil, &OTLPError{StatusCode: resp.StatusCode, RetryAfter: parseRetryAfter(resp)}
}

// OTLPError wraps an HTTP error from the collector.
type OTLPError struct {
	StatusCode int
	RetryAfter time.Duration // non-zero if Retry-After was present
}

func (e *OTLPError) Error() string {
	return fmt.Sprintf("otlp collector returned %d", e.StatusCode)
}

func (e *OTLPError) IsRetryable() bool {
	switch e.StatusCode {
	case 429, 502, 503, 504:
		return true
	}
	return false
}

func (e *OTLPError) IsPermanent() bool {
	return e.StatusCode >= 400 && e.StatusCode < 500 && e.StatusCode != 429
}

// encodeExportRequest manually encodes ExportTraceServiceRequest (field 1 = repeated ResourceSpans).
// This avoids importing the collector/trace/v1 package which depends on grpc.
func encodeExportRequest(spans []*tracepb.ResourceSpans) ([]byte, error) {
	var out []byte
	for _, rs := range spans {
		b, err := proto.Marshal(rs)
		if err != nil {
			return nil, err
		}
		out = protowire.AppendTag(out, 1, protowire.BytesType)
		out = protowire.AppendBytes(out, b)
	}
	return out, nil
}

func parseRetryAfter(resp *http.Response) time.Duration {
	ra := resp.Header.Get("Retry-After")
	if ra == "" {
		return 0
	}
	if secs, err := strconv.Atoi(ra); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(ra); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

// DrainOTLP delivers outbox rows to the OTLP collector, handling retries and dead-lettering.
func DrainOTLP(ctx context.Context, j *storage.Journal, destination string, sender *OTLPSender, allowed []string, limit int) (int, error) {
	if len(allowed) == 0 {
		return 0, errors.New("export field allowlist is required")
	}
	now := time.Now()
	deliveries, err := j.Lease(ctx, destination, limit, now)
	if err != nil {
		return 0, err
	}
	if len(deliveries) == 0 {
		return 0, nil
	}
	acked, sendErr := sender.Send(ctx, deliveries, allowed)
	ackedSet := make(map[string]bool, len(acked))
	for _, id := range acked {
		ackedSet[id] = true
	}
	if len(acked) > 0 {
		if err := j.Ack(ctx, destination, acked); err != nil {
			return 0, err
		}
	}
	var otlpErr *OTLPError
	if errors.As(sendErr, &otlpErr) {
		errCode := fmt.Sprintf("otlp_http_%d", otlpErr.StatusCode)
		for _, d := range deliveries {
			if !ackedSet[d.EventID] {
				if otlpErr.IsPermanent() {
					// exhaust retries to force dead-letter (10 = storage.maxAttempts)
					for i := 0; i < 10; i++ {
						j.Fail(ctx, destination, d.EventID, errCode, now)
					}
				} else {
					j.Fail(ctx, destination, d.EventID, errCode, now)
				}
			}
		}
	} else if sendErr != nil {
		for _, d := range deliveries {
			if !ackedSet[d.EventID] {
				j.Fail(ctx, destination, d.EventID, "otlp_send_error", now)
			}
		}
	}
	return len(acked), sendErr
}

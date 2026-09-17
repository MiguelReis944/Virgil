package export

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryAfter429(t *testing.T) {
	calls := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			return
		}
		io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer server.Close()
	_, j := openTestJournal(t)
	ev := appendTo(t, j, "otlp-dest")
	sender := NewOTLPSender(server.URL, nil, nil)
	deliveries, _ := j.Lease(context.Background(), "otlp-dest", 10, time.Now())
	_, err := sender.Send(context.Background(), deliveries, []string{"provider", "status"})
	var otlpErr *OTLPError
	if err == nil {
		t.Fatal("expected error on 429")
	}
	if otlpErr, _ = err.(*OTLPError); otlpErr == nil || otlpErr.StatusCode != 429 {
		t.Fatalf("expected OTLPError(429), got %v", err)
	}
	if otlpErr.RetryAfter != 2*time.Second {
		t.Fatalf("wrong retry-after: %v", otlpErr.RetryAfter)
	}
	if !otlpErr.IsRetryable() {
		t.Fatal("expected retryable")
	}
	_ = ev
	// Now send again and it should succeed; pass a future time to expire the first lease
	deliveries2, _ := j.Lease(context.Background(), "otlp-dest", 10, time.Now().Add(10*time.Minute))
	acked, err := sender.Send(context.Background(), deliveries2, []string{"provider", "status"})
	if err != nil || len(acked) == 0 {
		t.Fatalf("second send: %v acked=%d", err, len(acked))
	}
}

func TestPermanent400DeadLetter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
	}))
	defer server.Close()
	_, j := openTestJournal(t)
	appendTo(t, j, "otlp-perm")
	sender := NewOTLPSender(server.URL, nil, nil)
	n, err := DrainOTLP(context.Background(), j, "otlp-perm", sender, []string{"provider"}, 10)
	if err == nil || n != 0 {
		t.Fatalf("expected error and 0 acked: %v n=%d", err, n)
	}
	// After permanent failure, row should be dead_letter
	deliveries, _ := j.Lease(context.Background(), "otlp-perm", 10, time.Now())
	if len(deliveries) != 0 {
		t.Fatalf("expected dead-lettered row to not re-lease, got %d", len(deliveries))
	}
}

func TestCollectorReconnectDeliversOnce(t *testing.T) {
	delivered := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		delivered.Add(1)
		w.WriteHeader(200)
	}))
	defer server.Close()
	_, j := openTestJournal(t)
	appendTo(t, j, "otlp-reconnect")
	sender := NewOTLPSender(server.URL, nil, nil)
	n, err := DrainOTLP(context.Background(), j, "otlp-reconnect", sender, []string{"provider"}, 10)
	if err != nil || n != 1 {
		t.Fatalf("drain: %v n=%d", err, n)
	}
	// second drain should find nothing
	n2, err2 := DrainOTLP(context.Background(), j, "otlp-reconnect", sender, []string{"provider"}, 10)
	if err2 != nil || n2 != 0 {
		t.Fatalf("second drain: %v n=%d", err2, n2)
	}
	if delivered.Load() != 1 {
		t.Fatalf("expected 1 delivery to collector, got %d", delivered.Load())
	}
	_ = strconv.Itoa // avoid unused import
}

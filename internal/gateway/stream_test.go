package gateway

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGatewayStreamsBeforeProviderFinishes(t *testing.T) {
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"model\":\"fixture-model\",\"choices\":[]}\n\n")
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	handler := chatServer(t, upstream.URL, func(string) string { return "" })
	server := configuredTestServer(t, handler)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"fixture-model","messages":[{"role":"user","content":"synthetic"}],"stream":true}`))
	req.Header.Set("Authorization", "Bearer synthetic-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	first := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(resp.Body).ReadString('\n')
		first <- line
	}()
	select {
	case line := <-first:
		if !strings.Contains(line, "fixture-model") {
			t.Fatalf("first frame = %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gateway buffered the full stream")
	}
	close(release)
}

func TestClientCancelCancelsUpstream(t *testing.T) {
	upstreamCancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"model\":\"fixture-model\",\"choices\":[]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(upstreamCancelled)
	}))
	defer upstream.Close()
	handler := chatServer(t, upstream.URL, func(string) string { return "" })
	server := configuredTestServer(t, handler)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"fixture-model","messages":[{"role":"user","content":"synthetic"}],"stream":true}`))
	req.Header.Set("Authorization", "Bearer synthetic-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	_, _ = bufio.NewReader(resp.Body).ReadString('\n')
	cancel()
	resp.Body.Close()
	select {
	case <-upstreamCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream continued after client cancellation")
	}
}

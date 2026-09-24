package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/executions"
)

const maxControlResponse = 64 << 10
const maxSignalLine = 16 << 10

// ExecutionSummary is the durable result returned by the local core.
type ExecutionSummary struct {
	RunID                string                       `json:"run_id"`
	State                executions.State             `json:"state"`
	ExitCode             *int                         `json:"exit_code,omitempty"`
	StopReason           string                       `json:"stop_reason,omitempty"`
	TerminationStatus    executions.TerminationStatus `json:"termination_status,omitempty"`
	TerminationErrorCode string                       `json:"termination_error_code,omitempty"`
}

// ControlClient speaks only to an installation-local core.
type ControlClient struct {
	baseURL    string
	credential string
	client     *http.Client
}

// NewControlClient rejects hostnames and remote addresses before any credential is sent.
func NewControlClient(address, credential string) (*ControlClient, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("control address must be an HTTP loopback URL")
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || port == "" {
		return nil, errors.New("control address must include a loopback port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("control address must use a loopback IP")
	}
	if credential == "" {
		return nil, errors.New("control credential is required")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 5 * time.Second
	return &ControlClient{
		baseURL:    strings.TrimSuffix(u.String(), "/"),
		credential: credential,
		client:     &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func (c *ControlClient) request(ctx context.Context, method, path string, body any, signalToken string) (*http.Response, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, errors.New("encode control request")
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return nil, errors.New("create control request")
	}
	req.Header.Set("Authorization", "Bearer "+c.credential)
	if signalToken != "" {
		req.Header.Set("X-Virgil-Signal-Token", signalToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("local core unavailable: %w", err)
	}
	return res, nil
}

func decodeControlResponse(res *http.Response, want int, target any) error {
	defer res.Body.Close()
	if res.StatusCode != want {
		return fmt.Errorf("local core returned status %d", res.StatusCode)
	}
	if target == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxControlResponse+1))
	if err != nil {
		return errors.New("read local core response")
	}
	if len(data) > maxControlResponse {
		return errors.New("local core response too large")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return errors.New("invalid local core response")
	}
	return nil
}

func (c *ControlClient) Register(ctx context.Context, runID string) (executions.Registration, error) {
	res, err := c.request(ctx, http.MethodPost, "/api/executions", executions.RegisterRequest{RunID: runID}, "")
	if err != nil {
		return executions.Registration{}, err
	}
	var registration executions.Registration
	if err := decodeControlResponse(res, http.StatusCreated, &registration); err != nil {
		return executions.Registration{}, err
	}
	if registration.RunID == "" || registration.RunToken == "" || registration.SignalToken == "" || (runID != "" && registration.RunID != runID) {
		return executions.Registration{}, errors.New("invalid registration from local core")
	}
	return registration, nil
}

func (c *ControlClient) MarkRunning(ctx context.Context, runID string) error {
	res, err := c.request(ctx, http.MethodPost, "/api/executions/"+url.PathEscape(runID)+"/running", executions.RunningRequest{}, "")
	if err != nil {
		return err
	}
	return decodeControlResponse(res, http.StatusNoContent, nil)
}

func (c *ControlClient) Finish(ctx context.Context, runID string, result executions.FinishRequest) (ExecutionSummary, error) {
	res, err := c.request(ctx, http.MethodPost, "/api/executions/"+url.PathEscape(runID)+"/finish", result, "")
	if err != nil {
		return ExecutionSummary{}, err
	}
	var summary ExecutionSummary
	if err := decodeControlResponse(res, http.StatusOK, &summary); err != nil {
		return ExecutionSummary{}, err
	}
	if summary.RunID != runID || summary.State == "" || summary.ExitCode == nil {
		return ExecutionSummary{}, errors.New("invalid execution summary from local core")
	}
	return summary, nil
}

// Signals returns only after the ready event. The caller cancels ctx to release
// the stream; unexpected closure or malformed events appear on failures.
func (c *ControlClient) Signals(ctx context.Context, registration executions.Registration) (<-chan executions.Signal, <-chan error, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	readyTimer := time.AfterFunc(5*time.Second, cancel)
	res, err := c.request(streamCtx, http.MethodGet, "/api/executions/"+url.PathEscape(registration.RunID)+"/signals", nil, registration.SignalToken)
	if err != nil {
		readyTimer.Stop()
		cancel()
		return nil, nil, err
	}
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		readyTimer.Stop()
		cancel()
		res.Body.Close()
		return nil, nil, fmt.Errorf("local core signal stream returned status %d", res.StatusCode)
	}
	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 4096), maxSignalLine)
	event, data, err := readSignalEvent(scanner)
	readyTimer.Stop()
	if err != nil || event != "ready" || data != "" {
		cancel()
		res.Body.Close()
		return nil, nil, errors.New("local core signal stream was not ready")
	}
	signals := make(chan executions.Signal, 1)
	failures := make(chan error, 1)
	go func() {
		defer cancel()
		defer res.Body.Close()
		seen := false
		for {
			event, data, err := readSignalEvent(scanner)
			if err != nil {
				if ctx.Err() == nil && !seen {
					failures <- fmt.Errorf("local core signal stream lost: %w", err)
				}
				return
			}
			if event != "circuit_break" || seen {
				failures <- errors.New("invalid local core signal event")
				return
			}
			var notice executions.PolicyBlockNotice
			if err := json.Unmarshal([]byte(data), &notice); err != nil || notice.RunID != registration.RunID {
				failures <- errors.New("invalid local core circuit break")
				return
			}
			seen = true
			signals <- executions.Signal{Kind: executions.SignalCircuitBreak, PolicyBlock: notice}
		}
	}()
	return signals, failures, nil
}

func readSignalEvent(scanner *bufio.Scanner) (string, string, error) {
	var event, data string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if event == "" && data == "" {
				continue
			}
			return event, data, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			return "", "", errors.New("malformed signal line")
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			if event != "" {
				return "", "", errors.New("duplicate signal event")
			}
			event = value
		case "data":
			if data != "" {
				return "", "", errors.New("duplicate signal data")
			}
			data = value
		default:
			return "", "", errors.New("unknown signal field")
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	return "", "", io.EOF
}

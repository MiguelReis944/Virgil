package telemetry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"
)

const SchemaVersion = "1.0"

type PolicyDecision struct {
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
	Policy    string `json:"policy"`
	Attempt   int64  `json:"attempt"`
	Threshold int64  `json:"threshold"`
}

type Event struct {
	EventID        string          `json:"event_id"`
	EventType      string          `json:"event_type"`
	SchemaVersion  string          `json:"schema_version"`
	CreatedAt      time.Time       `json:"created_at"`
	OrganizationID string          `json:"organization_id,omitempty"`
	InstallationID string          `json:"installation_id"`
	ProjectID      string          `json:"project_id,omitempty"`
	Environment    string          `json:"environment,omitempty"`
	AgentID        string          `json:"agent_id,omitempty"`
	RunID          string          `json:"run_id"`
	TraceID        string          `json:"trace_id"`
	SpanID         string          `json:"span_id"`
	Provider       string          `json:"provider"`
	RequestedModel string          `json:"requested_model"`
	ResponseModel  string          `json:"response_model,omitempty"`
	InputTokens    *int64          `json:"input_tokens"`
	OutputTokens   *int64          `json:"output_tokens"`
	CachedTokens   *int64          `json:"cached_tokens"`
	UsageSource    string          `json:"usage_source"`
	LatencyMS      int64           `json:"latency_ms"`
	Status         string          `json:"status"`
	ErrorCode      string          `json:"error_code,omitempty"`
	ToolName       string          `json:"tool_name,omitempty"`
	PolicyDecision *PolicyDecision `json:"policy_decision,omitempty"`
	ActualCost     *string         `json:"actual_cost"`
	EstimatedCost  *string         `json:"estimated_cost"`
	PricingVersion string          `json:"pricing_version,omitempty"`
	CostCurrency   string          `json:"cost_currency,omitempty"`
	ContentCapture bool            `json:"content_capture"`
}

type Attempt struct {
	OrganizationID string
	InstallationID string
	ProjectID      string
	Environment    string
	AgentID        string
	RunID          string
	TraceID        string
	SpanID         string
	Provider       string
	RequestedModel string
	ResponseModel  string
	InputTokens    *int64
	OutputTokens   *int64
	CachedTokens   *int64
	UsageSource    string
	Status         string
	ErrorCode      string
	ToolName       string
	PolicyDecision *PolicyDecision
	ActualCost     *string
	EstimatedCost  *string
	PricingVersion string
	CostCurrency   string
	StartedAt      time.Time
	EndedAt        time.Time
}

var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)
var errorCodePattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
var modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

func validCount(count *int64) bool {
	return count == nil || *count >= 0
}

func validOptionalLabel(value string) bool {
	return value == "" || labelPattern.MatchString(value)
}

func Build(a Attempt) (Event, error) {
	if !labelPattern.MatchString(a.InstallationID) || !labelPattern.MatchString(a.RunID) ||
		!validHexID(a.TraceID, 32) || !validHexID(a.SpanID, 16) ||
		!labelPattern.MatchString(a.Provider) || !modelPattern.MatchString(a.RequestedModel) ||
		(a.ResponseModel != "" && !modelPattern.MatchString(a.ResponseModel)) ||
		!validOptionalLabel(a.OrganizationID) || !validOptionalLabel(a.ProjectID) ||
		!validOptionalLabel(a.Environment) || !validOptionalLabel(a.AgentID) ||
		(a.ToolName != "" && !modelPattern.MatchString(a.ToolName)) ||
		!validOptionalLabel(a.PricingVersion) ||
		(a.CostCurrency != "" && !currencyPattern.MatchString(a.CostCurrency)) {
		return Event{}, errors.New("invalid event identity")
	}
	if a.EndedAt.Before(a.StartedAt) || a.StartedAt.IsZero() {
		return Event{}, errors.New("invalid event timing")
	}
	if !validCount(a.InputTokens) || !validCount(a.OutputTokens) || !validCount(a.CachedTokens) {
		return Event{}, errors.New("negative token count")
	}
	if a.CachedTokens != nil && (a.InputTokens == nil || *a.CachedTokens > *a.InputTokens) {
		return Event{}, errors.New("cached tokens exceed input tokens")
	}
	switch a.Status {
	case "success", "provider_error", "transport_error", "policy_block", "client_cancelled":
	default:
		return Event{}, errors.New("invalid event status")
	}
	switch a.UsageSource {
	case "provider", "estimated":
		if a.InputTokens == nil && a.OutputTokens == nil {
			return Event{}, errors.New("usage source has no counts")
		}
	case "unknown":
		if a.InputTokens != nil || a.OutputTokens != nil || a.CachedTokens != nil {
			return Event{}, errors.New("unknown usage has counts")
		}
	default:
		return Event{}, errors.New("invalid usage source")
	}
	if a.ErrorCode != "" && !errorCodePattern.MatchString(a.ErrorCode) {
		return Event{}, errors.New("invalid error code")
	}
	if a.ActualCost != nil && a.EstimatedCost != nil {
		return Event{}, errors.New("actual and estimated cost are exclusive")
	}
	if a.ActualCost != nil && (a.UsageSource != "provider" || !decimalPattern.MatchString(*a.ActualCost)) {
		return Event{}, errors.New("invalid actual cost")
	}
	if a.EstimatedCost != nil && (a.UsageSource != "estimated" || !decimalPattern.MatchString(*a.EstimatedCost)) {
		return Event{}, errors.New("invalid estimated cost")
	}
	id, err := NewID(16)
	if err != nil {
		return Event{}, fmt.Errorf("create event ID: %w", err)
	}
	eventType := "llm.response.completed"
	if a.Status == "policy_block" {
		eventType = "llm.policy.blocked"
	} else if a.Status != "success" {
		eventType = "llm.response.failed"
	}
	return Event{
		EventID: id, EventType: eventType, SchemaVersion: SchemaVersion,
		CreatedAt: a.EndedAt.UTC(), OrganizationID: a.OrganizationID,
		InstallationID: a.InstallationID, ProjectID: a.ProjectID,
		Environment: a.Environment, AgentID: a.AgentID, RunID: a.RunID,
		TraceID: a.TraceID, SpanID: a.SpanID, Provider: a.Provider,
		RequestedModel: a.RequestedModel, ResponseModel: a.ResponseModel,
		InputTokens: a.InputTokens, OutputTokens: a.OutputTokens,
		CachedTokens: a.CachedTokens, UsageSource: a.UsageSource,
		LatencyMS: a.EndedAt.Sub(a.StartedAt).Milliseconds(), Status: a.Status,
		ErrorCode: a.ErrorCode, ToolName: a.ToolName, PolicyDecision: a.PolicyDecision,
		ActualCost: a.ActualCost, EstimatedCost: a.EstimatedCost,
		PricingVersion: a.PricingVersion, CostCurrency: a.CostCurrency,
		ContentCapture: false,
	}, nil
}

func DecodeEvent(reader io.Reader) (Event, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var event Event
	if err := decoder.Decode(&event); err != nil {
		return Event{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Event{}, errors.New("extra JSON value")
	}
	if event.ContentCapture {
		return Event{}, errors.New("content capture is unsupported")
	}
	return event, nil
}

package export

import (
	"strconv"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/MiguelReis944/Virgil/internal/storage"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

const instrumentationScope = "virgil.gateway"
const instrumentationVersion = "1.0"

func deliveriesToResourceSpans(deliveries []storage.Delivery, allowed []string) []*tracepb.ResourceSpans {
	allowSet := make(map[string]bool, len(allowed))
	for _, f := range allowed {
		allowSet[f] = true
	}
	spans := make([]*tracepb.Span, 0, len(deliveries))
	for _, d := range deliveries {
		span := eventToSpan(d.Event, allowSet)
		if span != nil {
			spans = append(spans, span)
		}
	}
	if len(spans) == 0 {
		return nil
	}
	return []*tracepb.ResourceSpans{
		{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					kv("service.name", "virgil-gateway"),
				},
			},
			ScopeSpans: []*tracepb.ScopeSpans{
				{
					Scope: &commonpb.InstrumentationScope{
						Name:    instrumentationScope,
						Version: instrumentationVersion,
					},
					Spans: spans,
				},
			},
		},
	}
}

func eventToSpan(ev telemetry.Event, allowed map[string]bool) *tracepb.Span {
	traceBytes := hexToBytes(ev.TraceID, 16)
	spanBytes := hexToBytes(ev.SpanID, 8)
	if traceBytes == nil || spanBytes == nil {
		return nil
	}
	startNs := ev.CreatedAt.Add(-time.Duration(ev.LatencyMS) * time.Millisecond).UnixNano()
	endNs := ev.CreatedAt.UnixNano()
	status := &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK}
	if ev.Status != "success" {
		status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: ev.Status}
	}
	attrs := make([]*commonpb.KeyValue, 0, 12)
	add := func(key, field, val string) {
		if allowed[field] || allowed["*"] {
			attrs = append(attrs, kv(key, val))
		}
	}
	addInt := func(key, field string, val int64) {
		if allowed[field] || allowed["*"] {
			attrs = append(attrs, kvInt(key, val))
		}
	}
	add("virgil.provider", "provider", ev.Provider)
	add("virgil.requested_model", "requested_model", ev.RequestedModel)
	add("virgil.response_model", "response_model", ev.ResponseModel)
	add("virgil.usage_source", "usage_source", ev.UsageSource)
	add("virgil.run_id", "run_id", ev.RunID)
	add("virgil.installation_id", "installation_id", ev.InstallationID)
	add("virgil.status", "status", ev.Status)
	add("virgil.error_code", "error_code", ev.ErrorCode)
	if ev.InputTokens != nil {
		addInt("virgil.input_tokens", "input_tokens", *ev.InputTokens)
	}
	if ev.OutputTokens != nil {
		addInt("virgil.output_tokens", "output_tokens", *ev.OutputTokens)
	}
	if ev.CachedTokens != nil {
		addInt("virgil.cached_tokens", "cached_tokens", *ev.CachedTokens)
	}
	addInt("virgil.latency_ms", "latency_ms", ev.LatencyMS)
	if ev.ActualCost != nil {
		add("virgil.actual_cost", "actual_cost", *ev.ActualCost)
	}
	if ev.EstimatedCost != nil {
		add("virgil.estimated_cost", "estimated_cost", *ev.EstimatedCost)
	}
	if ev.PricingVersion != "" {
		add("virgil.pricing_version", "pricing_version", ev.PricingVersion)
	}
	if ev.PolicyDecision != nil {
		if allowed["policy_decision"] || allowed["*"] {
			attrs = append(attrs, kv("virgil.policy.decision", ev.PolicyDecision.Decision))
			attrs = append(attrs, kv("virgil.policy.reason", ev.PolicyDecision.Reason))
		}
	}
	return &tracepb.Span{
		TraceId:           traceBytes,
		SpanId:            spanBytes,
		Name:              ev.EventType,
		Kind:              tracepb.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: uint64(startNs),
		EndTimeUnixNano:   uint64(endNs),
		Attributes:        attrs,
		Status:            status,
	}
}

func kv(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func kvInt(k string, v int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v}}}
}

func hexToBytes(hex string, wantLen int) []byte {
	if len(hex) != wantLen*2 {
		return nil
	}
	b := make([]byte, wantLen)
	for i := 0; i < wantLen; i++ {
		v, err := strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil
		}
		b[i] = byte(v)
	}
	return b
}

package redaction

import (
	"encoding/json"
	"errors"

	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

var exportableFields = map[string]bool{
	"event_id": true, "event_type": true, "schema_version": true,
	"created_at": true, "organization_id": true, "installation_id": true,
	"project_id": true, "environment": true, "agent_id": true, "run_id": true,
	"trace_id": true, "span_id": true, "provider": true,
	"requested_model": true, "response_model": true, "input_tokens": true,
	"output_tokens": true, "cached_tokens": true, "usage_source": true,
	"latency_ms": true, "status": true, "error_code": true,
	"tool_name": true, "policy_decision": true, "actual_cost": true,
	"estimated_cost": true, "pricing_version": true, "cost_currency": true,
	"content_capture": true,
}

func Prepare(event telemetry.Event) (telemetry.Event, error) {
	if err := telemetry.ValidateEvent(event); err != nil {
		return telemetry.Event{}, err
	}
	return event, nil
}

func ForExport(event telemetry.Event, allowed []string) (map[string]any, error) {
	if err := ValidateFields(allowed); err != nil {
		return nil, err
	}
	prepared, err := Prepare(event)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(prepared)
	if err != nil {
		return nil, err
	}
	var all map[string]any
	if err := json.Unmarshal(encoded, &all); err != nil {
		return nil, err
	}
	selected := make(map[string]any, len(allowed))
	for _, field := range allowed {
		if value, exists := all[field]; exists {
			selected[field] = value
		}
	}
	return selected, nil
}

func ValidateFields(allowed []string) error {
	if len(allowed) == 0 {
		return errors.New("export field allowlist is required")
	}
	for _, field := range allowed {
		if !exportableFields[field] {
			return errors.New("unsupported export field")
		}
	}
	return nil
}

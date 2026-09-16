package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
)

type TraceContext struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
}

var labelPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func NewID(bytesCount int) (string, error) {
	raw := make([]byte, bytesCount)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func validHexID(value string, length int) bool {
	if len(value) != length || strings.Trim(value, "0") == "" {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func NewTrace(traceparent string) (TraceContext, error) {
	parts := strings.Split(strings.ToLower(traceparent), "-")
	traceID := ""
	parentSpanID := ""
	if len(parts) == 4 && len(parts[0]) == 2 && parts[0] != "ff" &&
		validHexID(parts[1], 32) && validHexID(parts[2], 16) && len(parts[3]) == 2 {
		if _, err := hex.DecodeString(parts[0] + parts[3]); err == nil {
			traceID = parts[1]
			parentSpanID = parts[2]
		}
	}
	if traceID == "" {
		var err error
		traceID, err = NewID(16)
		if err != nil {
			return TraceContext{}, err
		}
	}
	spanID, err := NewID(8)
	if err != nil {
		return TraceContext{}, err
	}
	return TraceContext{TraceID: traceID, SpanID: spanID, ParentSpanID: parentSpanID}, nil
}

func ResolveRunID(candidate string) (string, error) {
	if labelPattern.MatchString(candidate) {
		return candidate, nil
	}
	id, err := NewID(16)
	if err != nil {
		return "", err
	}
	return "run_" + id, nil
}

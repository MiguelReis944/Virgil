package export

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/controlplane"
)

func TestDrainControlPlaneSendsAndAcks(t *testing.T) {
	_, j := openTestJournal(t)
	ev := appendTo(t, j, "cp-dest")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Events []struct {
				EventID string `json:"event_id"`
			} `json:"events"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var ids []string
		for _, e := range req.Events {
			ids = append(ids, e.EventID)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"accepted_ids": ids})
	}))
	defer server.Close()

	client := controlplane.NewClient(server.URL, "cred", nil)
	n, err := DrainControlPlane(context.Background(), j, "cp-dest", client, []string{"event_id", "provider"}, 10)
	if err != nil || n != 1 {
		t.Fatalf("DrainControlPlane: %v n=%d", err, n)
	}
	_ = ev
}

func TestControlPlanePayloadHonorsAllowlist(t *testing.T) {
	_, j := openTestJournal(t)
	ev := appendTo(t, j, "cp-private")
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Events []struct {
				EventID string         `json:"event_id"`
				Payload map[string]any `json:"payload"`
			} `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if len(req.Events) != 1 {
			t.Errorf("events=%d", len(req.Events))
			return
		}
		payload = req.Events[0].Payload
		json.NewEncoder(w).Encode(map[string]any{"accepted_ids": []string{req.Events[0].EventID}})
	}))
	defer server.Close()
	client := controlplane.NewClient(server.URL, "cred", nil)
	n, err := DrainControlPlane(context.Background(), j, "cp-private", client, []string{"provider"}, 10)
	if err != nil || n != 1 {
		t.Fatalf("drain: %v n=%d", err, n)
	}
	if len(payload) != 1 || payload["provider"] != ev.Provider {
		t.Fatalf("payload=%v", payload)
	}
}

func TestControlPlaneRejectsEmptyAllowlistBeforeNetwork(t *testing.T) {
	_, j := openTestJournal(t)
	appendTo(t, j, "cp-empty")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := controlplane.NewClient(server.URL, "cred", nil)
	n, err := DrainControlPlane(context.Background(), j, "cp-empty", client, nil, 10)
	if err == nil || n != 0 || calls != 0 {
		t.Fatalf("drain: %v n=%d calls=%d", err, n, calls)
	}
}

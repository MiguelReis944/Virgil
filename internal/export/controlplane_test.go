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
			Events []struct{ EventID string `json:"event_id"` } `json:"events"`
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
	n, err := DrainControlPlane(context.Background(), j, "cp-dest", client, nil, 10)
	if err != nil || n != 1 {
		t.Fatalf("DrainControlPlane: %v n=%d", err, n)
	}
	_ = ev
}

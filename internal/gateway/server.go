package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/MiguelReis944/Virgil/internal/config"
)

type Dependencies struct {
	DB *sql.DB
}

func NewServer(_ config.Config, deps Dependencies) (http.Handler, error) {
	if deps.DB == nil {
		return nil, errors.New("database connection is required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusOK
		body := struct {
			Status  string `json:"status"`
			Storage string `json:"storage"`
		}{Status: "ready", Storage: "ready"}
		if err := deps.DB.PingContext(r.Context()); err != nil {
			status = http.StatusServiceUnavailable
			body.Status = "unready"
			body.Storage = "unready"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	})
	return mux, nil
}

func ListenAndServe(ctx context.Context, address string, handler http.Handler) error {
	srv := &http.Server{Addr: address, Handler: handler}
	errs := make(chan error, 1)
	go func() {
		errs <- srv.ListenAndServe()
	}()
	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		_ = srv.Shutdown(context.Background())
		return nil
	}
}

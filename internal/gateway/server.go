package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

type EventRecorder interface {
	Record(ctx context.Context, event telemetry.Event) error
}

type Dependencies struct {
	DB             *sql.DB
	Client         *http.Client
	Getenv         func(string) string
	Recorder       EventRecorder
	InstallationID string
}

func NewServer(cfg config.Config, deps Dependencies) (http.Handler, error) {
	if deps.DB == nil {
		return nil, errors.New("database connection is required")
	}
	router, err := newRouter(cfg, deps.Client, deps.Getenv)
	if err != nil {
		return nil, err
	}
	router.recorder = deps.Recorder
	router.installationID = deps.InstallationID
	if router.installationID == "" {
		id, err := telemetry.NewID(16)
		if err != nil {
			return nil, err
		}
		router.installationID = "install_" + id
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", router.chat)
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
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	return serve(ctx, listener, handler)
}

func serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	srv := newHTTPServer(listener.Addr().String(), handler)
	srv.BaseContext = func(net.Listener) context.Context { return ctx }
	errs := make(chan error, 1)
	go func() {
		errs <- srv.Serve(listener)
	}()
	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr := srv.Shutdown(shutdownCtx)
		if shutdownErr != nil {
			_ = srv.Close()
		}
		serveErr := <-errs
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return shutdownErr
	}
}

func newHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
}

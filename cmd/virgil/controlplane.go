package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/controlplane"
	"github.com/MiguelReis944/Virgil/internal/export"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func configuredControlPlaneClient(cfg config.ControlPlaneConfig) (*controlplane.Client, error) {
	raw, err := os.ReadFile(cfg.CredentialPath)
	if err != nil {
		return nil, err
	}
	credential := strings.TrimSpace(string(raw))
	if credential == "" {
		return nil, errors.New("empty control plane credential")
	}
	return controlplane.NewClient(strings.TrimRight(cfg.Endpoint, "/"), credential, nil), nil
}

func drainControlPlaneOnce(ctx context.Context, journal *storage.Journal, client *controlplane.Client, allowed []string) error {
	_, err := export.DrainControlPlane(ctx, journal, "controlplane", client, allowed, 100)
	return err
}

func runControlPlaneDelivery(ctx context.Context, journal *storage.Journal, client *controlplane.Client, allowed []string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := drainControlPlaneOnce(attemptCtx, journal, client, allowed)
		cancel()
		if err != nil {
			var revoked *controlplane.CredentialRevokedError
			if errors.As(err, &revoked) {
				slog.Warn("control plane credential revoked", "code", "controlplane_revoked")
				return
			}
			slog.Warn("control plane delivery deferred", "code", "controlplane_delivery_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/controlplane"
	"github.com/MiguelReis944/Virgil/internal/export"
	"github.com/MiguelReis944/Virgil/internal/policies"
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

func loadCachedControlPlanePolicy(ctx context.Context, journal *storage.Journal, engine *policies.Engine) error {
	version, payload, err := journal.LoadRemotePolicy(ctx)
	if err != nil || payload == nil {
		return err
	}
	var policy controlplane.PolicyEnvelope
	if err := json.Unmarshal(payload, &policy); err != nil {
		return err
	}
	if policy.Version != version {
		return errors.New("cached policy version mismatch")
	}
	return engine.ApplyRemotePolicy(policy)
}

func syncControlPlanePolicy(ctx context.Context, engine *policies.Engine, client *controlplane.Client, etag *string) error {
	policy, err := client.CurrentPolicy(ctx, *etag)
	if err != nil || policy.Version == 0 {
		return err
	}
	if policy.Version <= engine.RemoteVersion() {
		return nil
	}
	if err := engine.CacheAndApplyRemotePolicy(ctx, policy); err != nil {
		return err
	}
	*etag = policy.ETag
	return nil
}

func runControlPlaneDelivery(ctx context.Context, journal *storage.Journal, engine *policies.Engine, client *controlplane.Client, allowed []string) {
	const baseInterval = 5 * time.Second
	const maxInterval = 120 * time.Second
	interval := baseInterval
	var etag string
	var idleStreak int
	for {
		prevEtag := etag
		attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := syncControlPlanePolicy(attemptCtx, engine, client, &etag)
		var drained int
		if err == nil {
			drained, err = export.DrainControlPlane(attemptCtx, journal, "controlplane", client, allowed, 100)
		}
		cancel()
		if err != nil {
			var revoked *controlplane.CredentialRevokedError
			if errors.As(err, &revoked) {
				slog.Warn("control plane credential revoked", "code", "controlplane_revoked")
				return
			}
			slog.Warn("control plane delivery deferred", "code", "controlplane_delivery_failed")
		}
		// Backoff when idle: no events sent and ETag unchanged.
		if drained == 0 && etag == prevEtag {
			idleStreak++
			nextInterval := time.Duration(5*(1<<idleStreak)) * time.Second
			if nextInterval > maxInterval {
				nextInterval = maxInterval
			}
			interval = nextInterval
		} else {
			idleStreak = 0
			interval = baseInterval
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

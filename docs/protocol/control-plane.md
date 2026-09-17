# Virgil Control Plane Protocol

Version 1.0 · September 2026

The Virgil Edge Gateway speaks this protocol to an optional Control Plane.
The gateway never requires a Control Plane; its absence does not prevent local
proxying, policy enforcement, or event recording.

The full machine-readable definition is in
[`schemas/controlplane.v1.openapi.yaml`](../../schemas/controlplane.v1.openapi.yaml).

---

## Security model

All batch and policy endpoints require a bearer token obtained by enrolling the
installation. The server derives tenant identity from the credential; any
`organization_id` field in the event payload is informational only and is never
used for access-control decisions.

A revoked credential causes the gateway to stop making remote calls immediately.
Local proxying, policy enforcement using the last cached policy, and event
recording in the local journal continue uninterrupted.

---

## Endpoints

### `POST /v1/installations/enroll`

Exchange a single-use operator token for a scoped installation credential. No
bearer token is required for this endpoint.

**Request**
```json
{ "one_time_token": "<operator-issued token>" }
```

**Response 200**
```json
{
  "installation_id": "install_...",
  "credential": "<bearer token for subsequent calls>"
}
```

The credential must be stored securely (OS secret store or an explicitly
configured, gitignored local file). It must never appear in event payloads.

---

### `POST /v1/events/batch`

Submit up to 1000 telemetry events in one request.

**Request**
```json
{
  "events": [
    {
      "event_id": "<stable client-generated ID>",
      "payload": { "<redacted telemetry fields>" }
    }
  ]
}
```

Content fields (prompts, responses, tool arguments) are never included.

**Response 200** (may include partial rejections)
```json
{
  "accepted_ids": ["<event_id>", ...],
  "rejections": {
    "<event_id>": "<rejection_code>"
  }
}
```

The gateway acks accepted IDs in its outbox. Permanently rejected events
(permanent 4xx other than 429) are dead-lettered and not retried.
Retryable errors (429, 502–504) are retried with exponential backoff.

---

### `GET /v1/policies/current`

Fetch the active policy envelope for this installation.
Pass `If-None-Match: <etag>` to avoid unnecessary data transfer; a `304` response
means the policy is unchanged.

**Response 200**
```json
{
  "version": 42,
  "limits": {
    "max_calls_per_run": 100,
    "max_cost_per_run_usd": "0.50"
  }
}
```

The response body must match the `PolicyEnvelope` schema. The gateway always
enforces the **stricter** of local and remote values. The remote policy cannot:
- Raise a limit that is set locally
- Change endpoint or export configuration
- Enable content capture

Stale policies (version ≤ last applied version) are silently ignored.
If the Control Plane is unreachable, the gateway uses the last successfully
fetched policy.

---

### `POST /v1/installations/{id}/revoke` *(admin only)*

Revoke an installation's credential. Only Control Plane administrators may call
this endpoint.

**Request**
```json
{ "reason": "Compromised device detected" }
```

**Response 200** — Subsequent batch and policy calls with the revoked credential
will return 401, causing the gateway to stop remote calls.

---

## Deduplication

The server deduplicates on `(organization_id, installation_id, event_id)`.
Resubmitting an already-accepted event returns its ID in `accepted_ids` without
creating a duplicate record.

---

## Versioning

This document describes version 1 of the protocol. Breaking changes will
increment the major version in the `Accept` and `Content-Type` headers.
Additive changes (new optional response fields) are non-breaking.

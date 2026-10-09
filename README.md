# Playback Guard

Tracearr-style sharing detection for MuxCore: guard rules, violations, trust scores (Phase 3).

## Capabilities

- `playback.guard` — list/upsert/delete rules, violation list/ack, trust scores, session terminate
- `settings`

## Environment

| Variable | Default | Description |
|----------|---------|-------------|
| `PLAYBACK_GUARD_DB_PATH` | `/var/lib/muxcore-playback-guard/guard.db` | SQLite database |
| `PLAYBACK_GUARD_GRPC_ADDR` | `127.0.0.1:9561` | gRPC listen address (loopback by default) |
| `PLAYBACK_GUARD_MODULE_TOKEN` | — | Bearer token for sensitive RPCs when mesh TLS is enabled |
| `PLAYBACK_GUARD_NOTIFY_ON_VIOLATION` | `0` | `1`/`true` sends legacy notification on rule fire |
| `PLAYBACK_GUARD_INACTIVITY_SWEEP_SEC` | `3600` | Interval for background account-inactivity sweep |
| `ERASURE_SWEEP_INTERVAL` | `5m` | How often the user-erasure reconciler reads the identity provider's ledger (Go duration, clamped to 30s–24h, jittered) |
| `MUXCORE_GRPC_ADDR` | — | Core mesh for playback event subscription and the erasure reconciler |
| `MUXCORE_INSECURE_DISABLE_TLS` | — | Dev-only plaintext mesh + sidecar dials |

`notify_on_violation` is persisted in SQLite (`module_settings`) and survives restart via admin-ui `UpdateSetting`.

## User erasure (ADR-0035)

When the identity provider erases a user, this module erases its own data for that user id. The provider's erasure ledger is the only authority; no event, header or request erases anything. Once connected to core, the module finds the `identity` provider through core discovery, verifies its certificate CN, lists the ledger at startup and every `ERASURE_SWEEP_INTERVAL`, applies each tombstone it has not applied, checks that no row still carries the id, and acknowledges.

| Data | Disposition |
|------|-------------|
| `violations`, `trust_scores` keyed by the user id | Deleted |
| `user_aliases` whose canonical user is the id, or keyed by the id; rows reachable through those aliases (an operator asserted they are the same person) | Deleted |
| Rows keyed by a `name:` key or an external media-server id that no alias links to the user | **Retained.** They live in a different id space and nothing links them to this user. Operators delete a single user's history with playback-monitor `DeleteUserHistory` or merge the identity first |
| `erasure_applied` (erasure id, user id, tenant, time, counts; no username) | Retained, never pruned: it makes the module refuse late writes for that id after a restart |

The deletions and the `erasure_applied` row are written in one SQLite transaction. After an erasure is recorded, new violations, trust updates and merges naming that user id are refused. The module stores no tenant; user ids are globally unique.

## Rule params

| Rule type | Params |
|-----------|--------|
| `concurrent_streams` | `max_streams`, `action` / `terminate_on_violation` |
| `geo_restriction` | `mode` (`blocklist`/`allowlist`), `countries`, `action` |
| `simultaneous_locations` | `min_distance_km`, `action` |
| `impossible_travel` | `max_speed_kmh`, `action` |
| `device_velocity` | `window_hours`, `max_ips`, `action` |
| `account_inactivity` | `inactivity_value`, `inactivity_unit` (`days`/`weeks`/`months`), `action` |

`action=terminate` or `terminate_on_violation=1` stops the triggering bridge session via `TerminateSession`.

## Sensitive RPCs

`TerminateSession`, `MergeUsers`, and `ResetTrustScore` require mesh caller identity or `PLAYBACK_GUARD_MODULE_TOKEN` when TLS is enabled (same pattern as `notification-default`).

## Status

v0.1.0 — Tracearr-style guard rules with SQLite store, mesh playback subscription (with reconnect), `playback-monitor` session queries, concurrent-stream / geo / account evaluators, trust scores, user merge, optional violation notifications, and per-rule session terminate.

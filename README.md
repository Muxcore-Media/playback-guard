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
| `MUXCORE_GRPC_ADDR` | — | Core mesh for playback event subscription |
| `MUXCORE_INSECURE_DISABLE_TLS` | — | Dev-only plaintext mesh + sidecar dials |

`notify_on_violation` is persisted in SQLite (`module_settings`) and survives restart via admin-ui `UpdateSetting`.

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

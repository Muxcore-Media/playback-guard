# Playback Guard

Tracearr-style sharing detection for MuxCore: guard rules, violations, trust scores (Phase 3).

## Capabilities

- `playback.guard` — rules CRUD, violation list/ack
- `settings`

## Environment

| Variable | Default | Description |
|----------|---------|-------------|
| `PLAYBACK_GUARD_DB_PATH` | `/var/lib/muxcore-playback-guard/guard.db` | SQLite database |
| `PLAYBACK_GUARD_GRPC_ADDR` | `:9561` | gRPC listen address |
| `MUXCORE_GRPC_ADDR` | — | Core mesh for playback event subscription |

## Status

v0.1.0 — Tracearr-style guard rules with SQLite store, mesh playback subscription, `playback-monitor` session queries, concurrent-stream / geo / account evaluators, trust scores, user merge, and optional violation notifications (`PLAYBACK_GUARD_NOTIFY_ON_VIOLATION`).

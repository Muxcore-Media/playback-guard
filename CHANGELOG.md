# Changelog

## [Unreleased]

### Added
- User erasure reconciler (ADR-0035, roadmap T-M4-07 slice E6): the module pulls the identity provider's erasure ledger, deletes the erased user's `violations`, `trust_scores` and `user_aliases` rows (and the rows reachable through aliases whose canonical user is the id) in one transaction that also records the application in the new `erasure_applied` table, checks the post-condition, and acknowledges. After an erasure is recorded the module refuses new violations, trust updates and merges for that user id, across restarts.
- `ERASURE_SWEEP_INTERVAL` (default 5m, jittered).
- ADR-0015 upgrade snapshot `internal/testdata/upgrade/v0.1.2.db`; `erasure_applied` is created by the existing idempotent startup DDL.

### Changed
- Built on core v0.6.17 / sdk/go/module v0.6.7 (erasure contract).

## [0.1.2] - 2026-10-05

### Changed
- Built on core v0.6.14 / sdk/go/module v0.6.4: unregisters on shutdown and re-registers after core restarts (ADR-0022).

## [0.1.1] - 2026-10-05


### Changed
- Reported version comes from muxcore.json (ADR-0021); built on core v0.6.12 / sdk/go/module v0.6.3 (mesh enrollment, ADR-0017).

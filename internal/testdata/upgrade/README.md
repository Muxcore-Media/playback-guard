# Upgrade fixtures (ADR-0015)

`<tag>.db` is a database written by that tag's own code with the seed in
`seed_upgrade_test.go.txt`; `<tag>.schema.sql` is its schema. `upgrade_test.go`
opens each snapshot with the current code twice.

Regenerate from the umbrella checkout:

    scripts/upgrade-fixtures/snapshot.sh playback-guard <tag> internal <tag>

`v0.1.2` has no `erasure_applied` table; the upgrade creates it (ADR-0035).

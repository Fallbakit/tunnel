# Synced with fallbakit/platform

These files are copied **verbatim** from `internal/tunnel` in the private
`fallbakit/platform` repo (server side) and must stay byte-identical:

- `agent.go`
- `bootstrap.go`
- `bootstrap_test.go`
- `identity.go`
- `runner.go`

Edit them in `fallbakit/platform` first, then copy here (or vice-versa). The
platform repo runs a `tunnel-drift` CI check that fails when the two copies
diverge. Do not add agent-only comments to these files — it would trip the
check. Agent-only code (`dial.go`, `errors.go`, `context_reader.go`) lives here
only and is exempt.

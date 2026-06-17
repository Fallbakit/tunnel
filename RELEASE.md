# Release Checklist

1. Run `go test ./...`.
2. Build release binaries for Linux and macOS.
3. Generate checksums for every binary.
4. Sign update manifests with the Fallbakit Ed25519 release key when auto-update is enabled.
5. Build and push `ghcr.io/fallbakit/fallbakit-agent:<version>`.
6. Tag the repository with the same version.
7. Publish release notes with upgrade, rollback, and compatibility notes.

The agent is backwards-compatible with hosted API-key bootstrap. Legacy mTLS mode remains available for self-hosted deployments that explicitly configure certificates.

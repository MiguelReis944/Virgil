# Local circuit breaker release acceptance

Record the release tag, commit, tested OS/architecture, artifact checksums, test output, and tester for every candidate. Do not publish a GitHub Release until each applicable item is checked with evidence.

- [ ] Clean install from the archive on Windows, Linux, and macOS, with no Go or Docker installed; verify `virgil version` and SHA-256 checksums.
- [ ] Start without a config and finish setup through the browser. Open the panel while provider internet access is offline.
- [ ] Configure a provider and protection from the panel, restart, and confirm both persist.
- [ ] Start a child with `virgil run`; route a valid provider request through Virgil and confirm it succeeds and appears under Executions.
- [ ] Trigger a policy block and confirm a durable blocked outcome, a reason in the panel, and termination of the root process and descendants.
- [ ] Restart the core during an active execution and confirm recovery records an accurate terminal state.
- [ ] Send a unique privacy canary in prompt, response, and tool arguments; confirm it is absent from the SQLite database, export, and panel when capture is disabled.
- [ ] Reject missing, malformed, and expired run tokens at the gateway and control endpoints.
- [ ] Confirm dashboard history, Health, Settings, and Usage after a restart.
- [ ] Restore a stopped-core backup of TOML, credential, and SQLite data; confirm config and execution history.
- [ ] Three-OS CI passes with real supervised process tests for Windows, Linux, and macOS.
- [ ] `go vet ./...` passes.
- [ ] `go test -race -timeout 180s ./...` passes on a supported CI runner.
- [ ] `govulncheck ./...` passes with current vulnerability data, or each finding has an explicit resolution before release.
- [ ] Review archive contents and verify the five targets: Windows amd64, Linux amd64/arm64, macOS amd64/arm64.
- [ ] Verify the signed build provenance for every archive and `SHA256SUMS` against the release repository and commit.

The tag workflow builds downloadable GitHub Actions artifacts. It does not publish a GitHub Release or distribute packages automatically.

# Install Virgil locally

Virgil runs as one executable on Windows, Linux, or macOS. Go and Docker are not required. It serves its panel and gateway on `127.0.0.1:8787` by default. Keep this address on loopback; remote LAN access needs a separate secured setup.

## Download and verify

Download the archive for your operating system and CPU, plus `SHA256SUMS`, from the same release build. Check its SHA-256 value against the corresponding line before extraction. The GitHub Actions build stores these files as workflow artifacts; a public GitHub Release must be published separately.
The workflow also records signed build provenance. If the GitHub CLI is available, run `gh attestation verify <archive> --repo MiguelReis944/Virgil` to confirm the archive came from this repository's build.

On Windows (PowerShell):

```powershell
Get-FileHash .\virgil-vVERSION-windows-amd64.zip -Algorithm SHA256
Expand-Archive .\virgil-vVERSION-windows-amd64.zip -DestinationPath .\virgil
Set-Location .\virgil
.\virgil.exe version
```

On Linux or macOS, select the matching `linux` or `darwin` archive and `amd64` or `arm64` CPU:

```sh
sha256sum virgil-vVERSION-linux-amd64.tar.gz   # Linux; compare with SHA256SUMS
# macOS: shasum -a 256 virgil-vVERSION-darwin-arm64.tar.gz
mkdir virgil && tar -xzf virgil-vVERSION-linux-amd64.tar.gz -C virgil
cd virgil
./virgil version
```

Replace `vVERSION` and the platform in these examples with the artifact filename. Work in a folder where Virgil can create `virgil.toml`, `.env`, and `data/`. Keep this folder private to your OS user. Set `VIRGIL_HOME` to that folder's absolute path when you want to launch the same Virgil installation from [any project or harness](integrations/any-project.md).

## First start

Run `./virgil` on Linux/macOS or `.\virgil.exe` on Windows. When `virgil.toml` is absent, Virgil creates it and opens the Providers onboarding flow in the panel. Complete provider setup there, then restart Virgil to load the saved configuration. Open `http://127.0.0.1:8787/dashboard` if the browser did not open automatically. The panel provides provider configuration, protections, executions, health, settings, and usage. If you use a headless local server, pass `--no-open` and connect through a local browser on that machine.

Add one provider in the panel with its API endpoint, model, and credential environment variable. Set that variable for the Virgil process, then restart Virgil. Configure a low request limit under Protections for the first supervised test. The local core creates `data/virgil.db` and `data/control.token`; treat the token as a secret.

With the core still running, start a compatible agent from a second terminal:

```powershell
.\virgil.exe run --config .\virgil.toml -- .\your-agent.exe
```

```sh
./virgil run --config ./virgil.toml -- ./your-agent
```

The child must send its supported OpenAI-compatible requests to the gateway address supplied by Virgil. Provider traffic sent directly to the provider cannot be stopped by Virgil. Inspect the execution and block reason in the panel. `virgil run --deadline 30m -- <command>` also limits elapsed time.

## Backup and restore

Stop the core and supervised runs before copying the database; this gives a consistent SQLite snapshot. Back up `virgil.toml`, `.env` if used, and the whole `data/` directory, including `virgil.db`, any SQLite sidecar files, and `control.token`. Keep the archive private because it may contain credentials and local history. Restore these files into the same relative paths before restarting Virgil. Verify the execution history in the panel after restore.

Windows:

```powershell
Compress-Archive -Path .\virgil.toml,.\data,.\.env -DestinationPath .\virgil-backup.zip
```

If you have no `.env`, omit it from the command. Linux/macOS:

```sh
tar -czf virgil-backup.tar.gz virgil.toml data .env
```

Likewise, omit `.env` when absent. Never copy the database while the core is writing to it.

## Upgrade or uninstall

For an upgrade, stop the core and child runs, take a backup, verify the new archive checksum, and replace only the executable. Keep `virgil.toml`, `.env`, and `data/`; start the new binary and check Health and Executions. Keep the previous binary and backup until the upgrade is verified.

To uninstall, stop Virgil and remove its extracted executable and folder. Removing `data/` deletes local history and the control credential; retain your backup first if you may need them. Virgil does not install a system service automatically.

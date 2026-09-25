# Use Virgil with any project or harness

Install and run **one Virgil core per machine** in a private installation directory. Codex and Claude Code can then work in any unrelated project directory while their model requests use that core. Virgil's database, credentials, logs, and config stay with its installation; the agent's working directory stays in the project.

## One-time installation

Set `VIRGIL_HOME` to an **absolute path** that belongs to your OS user. Use the same value in every terminal that starts Virgil or a supervised agent. For example, in PowerShell:

```powershell
$virgilHome = Join-Path $env:LOCALAPPDATA 'Virgil'
[IO.Directory]::CreateDirectory($virgilHome) | Out-Null
[Environment]::SetEnvironmentVariable('VIRGIL_HOME', $virgilHome, 'User')
$env:VIRGIL_HOME = $virgilHome
virgil init
virgil
```

On Linux or macOS, use a private directory and export the same variable in shells that run the core or an agent:

```sh
export VIRGIL_HOME="$HOME/.local/share/virgil"
mkdir -p "$VIRGIL_HOME"
virgil init
virgil
```

Open a new terminal after setting the user environment variable. Put provider credentials in the `.env` file inside `VIRGIL_HOME`, set providers and limits in the Virgil panel, and restart the core. The `virgil` binary must be available on `PATH` or invoked by its absolute path. If `VIRGIL_HOME` is unset and `virgil.toml` exists beside the executable, Virgil uses that file; otherwise it uses the current directory for first-time setup. `--config <absolute-path>` explicitly selects a different installation.

Relative `storage.path` and control-plane credential paths inside `virgil.toml` are resolved against the configuration file's directory. The default `.env` is read from that directory too. As a result, opening another project cannot silently move Virgil's database or control token there.

## Use in any harness

Start the core once, then open a terminal at the root of any project. For Codex CLI, run the [custom Responses provider command](codex.md) through `virgil run`; the command-line `-c` values direct that Codex process to Virgil. For Claude Code CLI, run `virgil run -- claude` as described in the [Claude guide](claude-code.md). The runner keeps the current directory as the child's working directory and supplies a fresh run token. Repeat this from another project without installing Virgil into it.

The [Codex desktop provider](desktop.md) is configured in the user's `~/.codex/config.toml`, and [Claude desktop](claude-code.md) uses its Third-Party Inference screen. These user-level settings apply to local projects opened in those apps. Desktop model requests receive Virgil policy and telemetry, but desktop processes are not supervised child processes.

Keep provider keys and the desktop token outside each project. A local Claude Code `settings.json` can override environment variables supplied by `virgil run`; check `/status` before a pilot and remove conflicting gateway settings. In every client, verify a fresh model request in Virgil's **Usage** panel. Requests that bypass Virgil cannot be protected or recorded.

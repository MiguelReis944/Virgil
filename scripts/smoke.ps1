param(
    [Parameter(Mandatory = $true)]
    [string] $Binary
)

$ErrorActionPreference = 'Stop'
$binaryPath = (Resolve-Path -LiteralPath $Binary).Path
$tempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("virgil-smoke-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tempDir | Out-Null
$portListener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$portListener.Start()
$port = $portListener.LocalEndpoint.Port
$portListener.Stop()
$baseUrl = "http://127.0.0.1:$port"
$dbPath = (Join-Path $tempDir 'virgil.db').Replace('\', '/')
$configPath = Join-Path $tempDir 'virgil.toml'
Set-Content -LiteralPath $configPath -Encoding utf8 -Value @"
[server]
listen = "127.0.0.1:$port"
[storage]
path = "$dbPath"
"@
$process = $null
try {
    $process = Start-Process -FilePath $binaryPath -ArgumentList @('serve', '--no-open', '--config', $configPath) -PassThru -WindowStyle Hidden -RedirectStandardOutput (Join-Path $tempDir 'stdout.log') -RedirectStandardError (Join-Path $tempDir 'stderr.log')
    $ready = $false
    for ($attempt = 0; $attempt -lt 100; $attempt++) {
        if ($process.HasExited) { throw "Virgil exited before readiness: $($process.ExitCode)" }
        try {
            $health = Invoke-WebRequest -Uri "$baseUrl/health" -NoProxy -TimeoutSec 1
            if ($health.StatusCode -eq 200) { $ready = $true; break }
        } catch {
            Start-Sleep -Milliseconds 100
        }
    }
    if (-not $ready) { throw 'Virgil did not answer /health' }

    $dashboard = Invoke-WebRequest -Uri "$baseUrl/dashboard" -NoProxy -TimeoutSec 3
    if ($dashboard.StatusCode -ne 200 -or $dashboard.Content -notmatch 'Virgil') {
        throw 'Virgil did not serve the dashboard'
    }

    $credential = (Get-Content -Raw -LiteralPath (Join-Path $tempDir 'control.token')).Trim()
    $runId = 'smoke_' + [guid]::NewGuid().ToString('N')
    $headers = @{ Authorization = "Bearer $credential" }
    $registered = Invoke-WebRequest -Method Post -Uri "$baseUrl/api/executions" -NoProxy -TimeoutSec 3 -Headers $headers -ContentType 'application/json' -Body ("{`"run_id`":`"$runId`"}")
    $execution = $registered.Content | ConvertFrom-Json
    if ($registered.StatusCode -ne 201 -or $execution.run_id -ne $runId -or -not $execution.run_token) {
        throw 'Virgil did not register a synthetic execution'
    }
    Write-Host 'Virgil installed-binary smoke passed.'
} finally {
    if ($null -ne $process -and -not $process.HasExited) {
        Stop-Process -Id $process.Id -Force
        $process.WaitForExit()
    }
    Remove-Item -LiteralPath $tempDir -Recurse -Force
}

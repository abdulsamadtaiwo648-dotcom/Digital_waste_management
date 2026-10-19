param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('resident', 'management', 'admin')]
    [string]$Role,
    [switch]$UseNeon
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$local = Join-Path $root '.local'
if (-not $UseNeon -and [string]::IsNullOrWhiteSpace($env:DATABASE_URL)) {
    $password = (Get-Content (Join-Path $local 'db-password') -Raw).Trim()
    $env:DATABASE_URL = "postgres://dwmp:$password@127.0.0.1:5432/dwmp?sslmode=disable"
}
$env:APP_ENV = 'development'
$env:DEV_RESET_LINKS = 'true'
$env:PAYMENT_MODE = 'simulation'
if ($UseNeon) {
    if ([string]::IsNullOrWhiteSpace($env:DATABASE_URL)) { throw 'DATABASE_URL must be loaded before starting an API with -UseNeon.' }
    $env:DEV_RESET_LINKS = 'false'
    $env:PAYMENT_MODE = 'disabled'
}
$secretFile = Join-Path $local "$Role-session-secret"
if (-not (Test-Path $secretFile)) {
    [guid]::NewGuid().ToString('N') + [guid]::NewGuid().ToString('N') | Set-Content -NoNewline $secretFile
}
$env:SESSION_SECRET = (Get-Content $secretFile -Raw).Trim()
$env:SERVICE_ROLE = $Role
$env:PYTHON_PROCESSOR_URL = 'http://127.0.0.1:8090'

switch ($Role) {
    'resident' { $env:PORT = '8080'; $env:SERVICE_ORIGIN = 'http://localhost:5173' }
    'management' { $env:PORT = '8081'; $env:SERVICE_ORIGIN = 'http://localhost:5174' }
    'admin' { $env:PORT = '8082'; $env:SERVICE_ORIGIN = 'http://localhost:5175' }
}

Set-Location $root
go run ./backend/go

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$local = Join-Path $root '.local'
$postgresBin = Join-Path $local 'postgres\pgsql\bin'
$postgresData = Join-Path $local 'postgres\data'
$postgres = Join-Path $postgresBin 'postgres.exe'
$passwordFile = Join-Path $local 'db-password'

if (-not (Test-Path $postgres) -or -not (Test-Path $passwordFile)) {
    throw 'Local PostgreSQL is not initialized. Extract the PostgreSQL 15 binaries and initialize the project database first.'
}
$env:PATH = "$postgresBin;$env:PATH"

function Test-LocalPort([int]$port) {
    $client = [System.Net.Sockets.TcpClient]::new()
    try { $client.Connect('127.0.0.1', $port); return $true }
    catch { return $false }
    finally { $client.Dispose() }
}

function Get-LocalPortOwners([int]$port) {
    $owners = netstat.exe -ano -p tcp | ForEach-Object {
        if ($_ -match "^\s*TCP\s+\S+:$port\s+\S+\s+LISTENING\s+(\d+)\s*$") { [int]$Matches[1] }
    }
    return $owners | Sort-Object -Unique
}

function Wait-LocalPort([int]$port, [string]$service) {
    for ($attempt = 0; $attempt -lt 20; $attempt++) {
        if (Test-LocalPort $port) { return }
        Start-Sleep -Seconds 1
    }
    throw "$service did not start listening on port $port. Check the logs under .local."
}

if (-not (Test-LocalPort 5432)) {
    Start-Process -FilePath $postgres -ArgumentList "-D `"$postgresData`" -h 127.0.0.1 -p 5432" -WorkingDirectory $root -RedirectStandardOutput (Join-Path $local 'postgres.stdout.log') -RedirectStandardError (Join-Path $local 'postgres.stderr.log') -WindowStyle Hidden | Out-Null
    Wait-LocalPort 5432 'PostgreSQL'
}

$password = (Get-Content $passwordFile -Raw).Trim()
$env:DATABASE_URL = "postgres://dwmp:$password@127.0.0.1:5432/dwmp?sslmode=disable"
$env:PYTHON_PROCESSOR_URL = 'http://127.0.0.1:8090'
$env:APP_ENV = 'development'
$env:DEV_RESET_LINKS = 'true'
$env:PAYMENT_MODE = 'simulation'
$env:GOCACHE = Join-Path $local 'go-cache'
New-Item -ItemType Directory -Force $env:GOCACHE | Out-Null

$psql = Join-Path $postgresBin 'psql.exe'
& $psql -v ON_ERROR_STOP=1 -f (Join-Path $root 'db\migrations\006_resident_locations.sql') $env:DATABASE_URL
if ($LASTEXITCODE -ne 0) { throw 'Resident location migration could not be applied.' }
& $psql -v ON_ERROR_STOP=1 -f (Join-Path $root 'db\migrations\007_area_waste_fees.sql') $env:DATABASE_URL
if ($LASTEXITCODE -ne 0) { throw 'Area waste fee migration could not be applied.' }
& $psql -v ON_ERROR_STOP=1 -f (Join-Path $root 'db\migrations\008_managed_service_areas.sql') $env:DATABASE_URL
if ($LASTEXITCODE -ne 0) { throw 'Managed service-area migration could not be applied.' }

if (-not (Test-LocalPort 8090)) {
    Start-Process -FilePath (Get-Command python.exe).Source -ArgumentList 'backend/python/processor.py' -WorkingDirectory $root -RedirectStandardOutput (Join-Path $local 'processor.log') -RedirectStandardError (Join-Path $local 'processor-error.log') -WindowStyle Hidden | Out-Null
    Wait-LocalPort 8090 'Python processor'
}
foreach ($service in @(@{Role='resident';Port=8080},@{Role='management';Port=8081},@{Role='admin';Port=8082})) {
    $startService = -not (Test-LocalPort $service.Port)
    if (-not $startService) {
        try {
            $serviceInfo = Invoke-WebRequest -UseBasicParsing "http://localhost:$($service.Port)/service-info" -TimeoutSec 3 | Select-Object -ExpandProperty Content | ConvertFrom-Json
            $startService = $serviceInfo.service_role -ne $service.Role
        } catch { $startService = $true }
        if ($startService) {
            $owners = Get-LocalPortOwners $service.Port
            foreach ($owner in $owners) { Stop-Process -Id $owner -Force -ErrorAction SilentlyContinue }
        }
    }
    if ($startService) {
        Start-Process -FilePath 'powershell.exe' -ArgumentList @('-NoProfile','-ExecutionPolicy','Bypass','-File',(Join-Path $PSScriptRoot 'run-api.ps1'),'-Role',$service.Role) -WorkingDirectory $root -RedirectStandardOutput (Join-Path $local "$($service.Role)-api.log") -RedirectStandardError (Join-Path $local "$($service.Role)-api-error.log") -WindowStyle Hidden | Out-Null
        Wait-LocalPort $service.Port "$($service.Role) API"
    }
}
if (-not (Test-LocalPort 5173)) {
    Start-Process -FilePath (Get-Command npm.cmd).Source -ArgumentList @('run', 'dev', '--', '--host', '127.0.0.1') -WorkingDirectory (Join-Path $root 'frontend') -RedirectStandardOutput (Join-Path $local 'frontend.log') -RedirectStandardError (Join-Path $local 'frontend-error.log') -WindowStyle Hidden | Out-Null
    Wait-LocalPort 5173 'Frontend'
}
if (-not (Test-LocalPort 5174)) {
    Start-Process -FilePath (Get-Command npm.cmd).Source -ArgumentList @('run', 'dev', '--', '--host', '127.0.0.1', '--port', '5174', '--strictPort') -WorkingDirectory (Join-Path $root 'frontend') -RedirectStandardOutput (Join-Path $local 'internal-frontend.log') -RedirectStandardError (Join-Path $local 'internal-frontend-error.log') -WindowStyle Hidden | Out-Null
    Wait-LocalPort 5174 'Internal portal'
}
if (-not (Test-LocalPort 5175)) {
    Start-Process -FilePath (Get-Command npm.cmd).Source -ArgumentList @('run', 'dev', '--', '--host', '127.0.0.1', '--port', '5175', '--strictPort') -WorkingDirectory (Join-Path $root 'frontend') -RedirectStandardOutput (Join-Path $local 'admin-frontend.log') -RedirectStandardError (Join-Path $local 'admin-frontend-error.log') -WindowStyle Hidden | Out-Null
    Wait-LocalPort 5175 'Admin software'
}

Write-Output 'Frontend: http://localhost:5173'
Write-Output 'Management software: http://localhost:5174/management.html#/login'
Write-Output 'Admin software: http://localhost:5175/admin.html#/login'
Write-Output 'Resident API:   http://localhost:8080'
Write-Output 'Management API: http://localhost:8081'
Write-Output 'Admin API:      http://localhost:8082'

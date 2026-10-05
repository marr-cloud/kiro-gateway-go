<#
.SYNOPSIS
  Arranca, para o reinicia el kiro-gateway local, o muestra sus logs.

.DESCRIPTION
  Unica logica de arranque del gateway: la usan kclaude (scripts/kiro-claude.ps1)
  y el mod de Claude Code (claude-mod/: /kiro y el relanzado automatico).
  Escribe una linea legible y sale con 0 (bien) o 1 (fallo).

  El gateway se lanza con Win32_Process.Create (CIM), no con Start-Process:
  Start-Process le pasa al gateway los handles heredados de este proceso, y
  quien capture la salida del script (el mod, con $.process.run) se quedaria
  esperando hasta que el gateway muriera. Asi queda separado: sobrevive a
  claude y a esta consola.

  Variables opcionales:
    KIRO_GATEWAY_PORT  puerto por defecto (8000)
    KIRO_GATEWAY_EXE   ruta al binario (default <repo>\kiro-gateway.exe)

.EXAMPLE
  .\scripts\kiro-gateway.ps1 start
  .\scripts\kiro-gateway.ps1 restart -DebugMode all
  .\scripts\kiro-gateway.ps1 logs -Lines 50
#>
param(
    [Parameter(Mandatory, Position = 0)]
    [ValidateSet('start', 'stop', 'restart', 'logs')]
    [string]$Action,
    [ValidateSet('all', 'errors', 'off')]
    [string]$DebugMode,
    [int]$Port = $(if ($env:KIRO_GATEWAY_PORT) { [int]$env:KIRO_GATEWAY_PORT } else { 8000 }),
    [int]$Lines = 20
)
$ErrorActionPreference = 'Stop'

$root = Split-Path $PSScriptRoot -Parent
$exe = if ($env:KIRO_GATEWAY_EXE) { $env:KIRO_GATEWAY_EXE } else { Join-Path $root 'kiro-gateway.exe' }
$base = "http://127.0.0.1:$Port"
$logDir = Join-Path $env:LOCALAPPDATA 'kiro-gateway'
$log = Join-Path $logDir 'gateway.log'
$errLog = Join-Path $logDir 'gateway.err.log'

function Test-Gateway {
    try { (Invoke-WebRequest "$base/health" -TimeoutSec 2 -UseBasicParsing).StatusCode -eq 200 } catch { $false }
}

# Proceso que escucha en el puerto, o $null. Falla si no es kiro-gateway: nunca
# se para un proceso ajeno.
function Get-GatewayProcess {
    $conn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $conn) { return $null }
    $proc = Get-Process -Id $conn.OwningProcess -ErrorAction SilentlyContinue
    $name = [IO.Path]::GetFileNameWithoutExtension($exe)
    if ($proc -and $proc.ProcessName -ne $name) {
        throw "El puerto $Port lo usa $($proc.ProcessName) (pid $($proc.Id)), no $name."
    }
    return $proc
}

function Start-Gateway {
    if (Test-Gateway) { return "kiro-gateway ya corre en $base" }
    if (-not (Test-Path $exe)) { throw "No encuentro $exe. Compilalo con: task build" }
    New-Item -ItemType Directory -Force $logDir | Out-Null
    # El gateway lee .env y credentials.json del directorio de trabajo.
    $envPrefix = if ($DebugMode) { "set DEBUG_MODE=$DebugMode&& " } else { '' }
    # Los logs se acumulan (>>): un relanzado no borra la causa de la caida.
    # Cada arranque deja una cabecera para distinguirlos en `logs`.
    $header = "== $((Get-Date).ToString('o')) arranque (DEBUG_MODE=$(if ($DebugMode) { $DebugMode } else { '.env' }))"
    Add-Content -Path $log, $errLog -Value $header
    $cmd = "cmd.exe /d /c `"$envPrefix`"$exe`" --host 127.0.0.1 --port $Port >> `"$log`" 2>> `"$errLog`"`""
    $startup = New-CimInstance -ClassName Win32_ProcessStartup -ClientOnly -Property @{ ShowWindow = [uint16]0 }
    $r = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{
        CommandLine = $cmd; CurrentDirectory = $root; ProcessStartupInformation = $startup
    }
    if ($r.ReturnValue -ne 0) { throw "Win32_Process.Create devolvio $($r.ReturnValue)" }
    $deadline = (Get-Date).AddSeconds(20)
    while (-not (Test-Gateway)) {
        if ((Get-Date) -gt $deadline) { throw "kiro-gateway no respondio en $base/health. Revisa $errLog" }
        Start-Sleep -Milliseconds 300
    }
    $mode = if ($DebugMode) { " (DEBUG_MODE=$DebugMode)" } else { '' }
    return "kiro-gateway listo en $base$mode"
}

function Stop-Gateway {
    $proc = Get-GatewayProcess
    if (-not $proc) { return "kiro-gateway no corria en $base" }
    Stop-Process -Id $proc.Id -Force
    $deadline = (Get-Date).AddSeconds(10)
    while (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue) {
        if ((Get-Date) -gt $deadline) { throw "El puerto $Port sigue ocupado tras parar kiro-gateway." }
        Start-Sleep -Milliseconds 200
    }
    return "kiro-gateway parado"
}

function Show-Logs {
    foreach ($f in $log, $errLog) {
        "== $f"
        if (Test-Path $f) { Get-Content $f -Tail $Lines } else { '(no existe)' }
    }
}

try {
    switch ($Action) {
        'start' { Start-Gateway }
        'stop' { Stop-Gateway }
        'restart' { Stop-Gateway | Out-Null; Start-Gateway }
        'logs' { Show-Logs }
    }
    exit 0
} catch {
    "error: $($_.Exception.Message)"
    exit 1
}

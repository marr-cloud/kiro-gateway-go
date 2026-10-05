<#
.SYNOPSIS
  Lanza Claude Code contra el kiro-gateway local.

.DESCRIPTION
  Arranca kiro-gateway en segundo plano con scripts/kiro-gateway.ps1 si
  /health no responde, y ejecuta `claude` con el mod claude-mod/ cargado
  (--plugin-dir) y las variables de entorno del gateway puestas SOLO para ese
  proceso: la sesion de PowerShell queda como estaba al salir. La key se lee
  de PROXY_API_KEY en el .env del repo; nunca se escribe en settings.

  Variables opcionales:
    KIRO_GATEWAY_PORT  puerto del gateway (default 8000)
    KIRO_GATEWAY_EXE   ruta al binario (default <repo>\kiro-gateway.exe)

.EXAMPLE
  .\scripts\kiro-claude.ps1
  .\scripts\kiro-claude.ps1 --model sonnet -p "hola"
#>
$ErrorActionPreference = 'Stop'

$root = Split-Path $PSScriptRoot -Parent
$port = if ($env:KIRO_GATEWAY_PORT) { $env:KIRO_GATEWAY_PORT } else { '8000' }

function Get-ProxyApiKey {
    $envFile = Join-Path $root '.env'
    if (-not (Test-Path $envFile)) { throw "No existe $envFile (copia .env.example y define PROXY_API_KEY)." }
    $line = Get-Content $envFile | Where-Object { $_ -match '^\s*PROXY_API_KEY\s*=' } | Select-Object -First 1
    if (-not $line) { throw "PROXY_API_KEY no esta definido en $envFile." }
    return ($line -replace '^\s*PROXY_API_KEY\s*=\s*', '').Trim().Trim('"', "'")
}

& (Join-Path $PSScriptRoot 'kiro-gateway.ps1') start -Port $port
if ($LASTEXITCODE -ne 0) { exit 1 }

$vars = @{
    ANTHROPIC_BASE_URL                         = "http://127.0.0.1:$port"
    ANTHROPIC_AUTH_TOKEN                       = (Get-ProxyApiKey)
    ANTHROPIC_API_KEY                          = $null
    CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY = '1'
    # Sin esto, el bloque de atribucion llega a Kiro como texto del system prompt.
    CLAUDE_CODE_ATTRIBUTION_HEADER             = '0'
}
$saved = @{}
foreach ($name in $vars.Keys) {
    $saved[$name] = [Environment]::GetEnvironmentVariable($name)
    [Environment]::SetEnvironmentVariable($name, $vars[$name])
}
try {
    & claude --plugin-dir (Join-Path $root 'claude-mod') @args
    $code = $LASTEXITCODE
} finally {
    foreach ($name in $saved.Keys) { [Environment]::SetEnvironmentVariable($name, $saved[$name]) }
}
exit $code

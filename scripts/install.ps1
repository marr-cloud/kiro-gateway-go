<#
.SYNOPSIS
  Deja kiro-gateway listo para usarlo con Claude Code: tras esto basta `kclaude`.

.DESCRIPTION
  Sirve igual desde un clon del repo que desde el zip de Windows de Releases
  (o desde scripts/get.ps1, que baja ese zip y lo ejecuta). Se puede repetir:
  solo crea lo que falta y nunca pisa un .env ni un credentials.json existentes.

    1. kiro-gateway.exe: si falta, lo compila con Go (solo en un clon).
    2. .env: lo crea desde .env.example con una PROXY_API_KEY aleatoria, que
       no se muestra en pantalla.
    3. credentials.json: lo crea con la cuenta de Kiro que encuentre en este
       equipo (kiro-cli, o si no Kiro IDE).
    4. $PROFILE: anade (o actualiza) una funcion kclaude que apunta a este
       directorio, entre dos marcas.
    5. Avisa si falta `claude` en el PATH.

.PARAMETER Build
  Recompila kiro-gateway.exe aunque ya exista (clon con Go, tras un git pull).

.PARAMETER SkipProfile
  No toca $PROFILE.

.EXAMPLE
  pwsh scripts/install.ps1
  pwsh scripts/install.ps1 -Build
#>
#Requires -Version 7
param(
    [switch]$Build,
    [switch]$SkipProfile
)
$ErrorActionPreference = 'Stop'

$root = Split-Path $PSScriptRoot -Parent
$exe = Join-Path $root 'kiro-gateway.exe'

function Say([string]$msg) { Write-Host "  $msg" }

# --- 1. Binario -------------------------------------------------------------
if ($Build -or -not (Test-Path $exe)) {
    if (-not (Test-Path (Join-Path $root 'go.mod'))) {
        throw "Falta $exe. Vuelve a descargar el zip de Windows de https://github.com/marr-cloud/kiro-gateway-go/releases"
    }
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw "Falta kiro-gateway.exe y no hay Go para compilarlo. Instala Go 1.27+ o usa el zip de Releases: irm https://raw.githubusercontent.com/marr-cloud/kiro-gateway-go/main/scripts/get.ps1 | iex"
    }
    $version = 'dev'
    if (Get-Command git -ErrorAction SilentlyContinue) {
        $described = git -C $root describe --tags --dirty 2>$null
        if ($LASTEXITCODE -eq 0 -and $described) { $version = $described }
    }
    Say "Compilando kiro-gateway.exe ($version)..."
    go build -C $root -trimpath `
        -ldflags "-s -w -X github.com/marr-cloud/kiro-gateway-go/internal/version.build=$version" `
        -o kiro-gateway.exe ./cmd/kiro-gateway
    if ($LASTEXITCODE -ne 0) { throw 'go build fallo (si el gateway esta corriendo, paralo antes: scripts/kiro-gateway.ps1 stop).' }
    Say 'kiro-gateway.exe compilado.'
} else {
    Say 'kiro-gateway.exe ya existe.'
}

# --- 2. .env ----------------------------------------------------------------
$envFile = Join-Path $root '.env'
if (Test-Path $envFile) {
    Say '.env ya existe: no se toca.'
} else {
    $key = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(24)).ToLower()
    $lines = Get-Content (Join-Path $root '.env.example') | ForEach-Object {
        if ($_ -match '^\s*PROXY_API_KEY\s*=') { "PROXY_API_KEY=`"$key`"" } else { $_ }
    }
    Set-Content -Path $envFile -Value $lines
    Say '.env creado con una PROXY_API_KEY aleatoria (kclaude la lee de ahi).'
}

# --- 3. credentials.json ----------------------------------------------------
$credFile = Join-Path $root 'credentials.json'
if (Test-Path $credFile) {
    Say 'credentials.json ya existe: no se toca.'
} else {
    # Una sola fuente: si son la misma cuenta, dos entradas solo duplican refrescos.
    $sources = @(
        @{ type = 'sqlite'; path = (Join-Path $env:LOCALAPPDATA 'kiro-cli\data.sqlite3'); name = 'kiro-cli' },
        @{ type = 'json'; path = (Join-Path $HOME '.aws\sso\cache\kiro-auth-token.json'); name = 'Kiro IDE' }
    )
    $found = $sources | Where-Object { Test-Path $_.path } | Select-Object -First 1
    if (-not $found) {
        throw "No encuentro credenciales de Kiro. Inicia sesion en kiro-cli (kiro-cli login) o en Kiro IDE y vuelve a ejecutar este script. Tambien puedes crear credentials.json a mano desde credentials.json.example."
    }
    $entry = [ordered]@{ type = $found.type; enabled = $true; path = ($found.path -replace '\\', '/') }
    ConvertTo-Json -InputObject @($entry) | Set-Content -Path $credFile
    Say "credentials.json creado con la cuenta de $($found.name)."
}

# --- 4. kclaude en $PROFILE -------------------------------------------------
if ($SkipProfile) {
    Say '$PROFILE no se toca (-SkipProfile).'
} else {
    $begin = '# >>> kiro-gateway >>>'
    $end = '# <<< kiro-gateway <<<'
    $launcher = Join-Path $root 'scripts\kiro-claude.ps1'
    $block = @(
        $begin
        "function kclaude { & '$($launcher -replace "'", "''")' @args }"
        $end
    ) -join [Environment]::NewLine
    $current = if (Test-Path $PROFILE) { Get-Content $PROFILE -Raw } else { '' }
    if ($null -eq $current) { $current = '' }
    $pattern = "(?s)$([regex]::Escape($begin)).*?$([regex]::Escape($end))"
    # Un kclaude propio fuera de las marcas (function o Set-Alias) se respeta: un alias
    # ganaria a la funcion y dos definiciones solo confunden.
    $own = [regex]::Replace($current, $pattern, '') -split "`r?`n" |
        Select-String -Pattern '^\s*(function\s+kclaude\b|(Set|New)-Alias\s+(-Name\s+)?kclaude\b)' |
        Select-Object -First 1
    if ($own) {
        Say "Ya tienes kclaude en $PROFILE (linea propia): no se toca."
        if ($own.Line -notlike "*$launcher*") { Say "  Para este directorio deberia apuntar a $launcher" }
    } elseif ($current -match $pattern) {
        $updated = [regex]::Replace($current, $pattern, { $block })
        if ($updated -ne $current) {
            Set-Content -Path $PROFILE -Value $updated -NoNewline
            Say "kclaude actualizado en $PROFILE."
        } else {
            Say "kclaude ya esta en $PROFILE."
        }
    } else {
        New-Item -ItemType Directory -Force (Split-Path $PROFILE -Parent) | Out-Null
        $sep = if ($current -and -not $current.EndsWith("`n")) { [Environment]::NewLine } else { '' }
        Add-Content -Path $PROFILE -Value "$sep$block"
        Say "kclaude anadido a $PROFILE."
    }
}

# --- 5. Comprobaciones ------------------------------------------------------
if (-not (Get-Command claude -ErrorAction SilentlyContinue)) {
    Say 'AVISO: no encuentro `claude` en el PATH. Instala Claude Code: https://docs.claude.com/claude-code'
}

Write-Host ''
if ($SkipProfile) {
    Say "Listo. Ejecuta: $(Join-Path $root 'scripts\kiro-claude.ps1')"
} else {
    Say 'Listo. Abre una terminal nueva de pwsh (o ejecuta . $PROFILE) y escribe: kclaude'
}

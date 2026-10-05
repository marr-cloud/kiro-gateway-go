<#
.SYNOPSIS
  Instala (o actualiza) kiro-gateway para Windows desde la ultima release.

.DESCRIPTION
  Pensado para ejecutarse en una linea, desde Windows PowerShell o pwsh:

    irm https://raw.githubusercontent.com/marr-cloud/kiro-gateway-go/main/scripts/get.ps1 | iex

  Baja el zip de Windows de la ultima release, comprueba su SHA256 contra
  SHA256SUMS, lo copia en %LOCALAPPDATA%\Programs\kiro-gateway y ejecuta
  scripts/install.ps1 con pwsh. Al actualizar conserva .env, credentials.json
  y state.json, que no vienen en el zip.

  Todo va dentro de un bloque & { }: ni $ErrorActionPreference ni las variables
  quedan en la sesion de quien lo ejecuta con iex, y los errores son throw, no
  exit, para no cerrarle la terminal.
#>
& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue' # Invoke-WebRequest va mucho mas rapido sin barra
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $repo = 'marr-cloud/kiro-gateway-go'
    $dest = Join-Path $env:LOCALAPPDATA 'Programs\kiro-gateway'

    if (-not (Get-Command pwsh -ErrorAction SilentlyContinue)) {
        throw 'kiro-gateway necesita PowerShell 7 (pwsh). Instalalo con: winget install Microsoft.PowerShell, y vuelve a ejecutar esta linea.'
    }

    $release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest" -UseBasicParsing
    $tag = $release.tag_name
    $name = "kiro-gateway_${tag}_windows_amd64"
    $base = "https://github.com/$repo/releases/download/$tag"
    Write-Host "  Descargando kiro-gateway $tag..."

    $tmp = Join-Path ([IO.Path]::GetTempPath()) "kiro-gateway-$([guid]::NewGuid().ToString('N'))"
    New-Item -ItemType Directory -Force $tmp | Out-Null
    try {
        $zip = Join-Path $tmp "$name.zip"
        Invoke-WebRequest "$base/$name.zip" -OutFile $zip -UseBasicParsing
        $sums = (Invoke-WebRequest "$base/SHA256SUMS" -UseBasicParsing).Content
        if ($sums -is [byte[]]) { $sums = [Text.Encoding]::UTF8.GetString($sums) }
        $line = ($sums -split "`n") | Where-Object { $_ -match "\s\*?$([regex]::Escape("$name.zip"))\s*$" } | Select-Object -First 1
        if (-not $line) { throw "SHA256SUMS de $tag no lista $name.zip." }
        $expected = ($line -split '\s+')[0].ToLower()
        # .NET y no Get-FileHash/Expand-Archive: Windows PowerShell lanzado con el PSModulePath
        # de pwsh 7 (p.ej. desde Git Bash) carga los modulos de pwsh y no los encuentra.
        $sha = [Security.Cryptography.SHA256]::Create()
        $fs = [IO.File]::OpenRead($zip)
        try { $actual = -join ($sha.ComputeHash($fs) | ForEach-Object { $_.ToString('x2') }) } finally { $fs.Dispose(); $sha.Dispose() }
        if ($actual -ne $expected) { throw "El SHA256 de $name.zip no coincide con SHA256SUMS ($actual != $expected)." }

        Add-Type -AssemblyName System.IO.Compression.FileSystem
        [IO.Compression.ZipFile]::ExtractToDirectory($zip, $tmp)
        $src = Join-Path $tmp $name

        # Un gateway en marcha bloquea kiro-gateway.exe: se para y kclaude lo vuelve a arrancar.
        $stop = Join-Path $dest 'scripts\kiro-gateway.ps1'
        if (Test-Path $stop) { pwsh -NoProfile -NonInteractive -File $stop stop | Out-Null }

        New-Item -ItemType Directory -Force $dest | Out-Null
        # scripts/ y claude-mod/ se reemplazan enteros para no dejar ficheros de una version vieja.
        foreach ($dir in 'scripts', 'claude-mod') {
            $old = Join-Path $dest $dir
            if (Test-Path $old) { Remove-Item $old -Recurse -Force }
        }
        Copy-Item (Join-Path $src '*') $dest -Recurse -Force
    } finally {
        Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }

    Write-Host "  kiro-gateway $tag en $dest"
    pwsh -NoProfile -File (Join-Path $dest 'scripts\install.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'scripts/install.ps1 no termino bien (mira el mensaje de arriba).' }
}

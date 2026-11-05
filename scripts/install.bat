@echo off
setlocal EnableExtensions

chcp 65001 >nul
pushd "%~dp0.."

where powershell.exe >nul 2>&1
if errorlevel 1 (
    echo [ERROR] Windows PowerShell was not found on PATH.
    popd
    pause
    exit /b 1
)

set "INSTALL_SELF=%~f0"

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -Command "$m = '#:PSSTART'; $s = Get-Content -LiteralPath $env:INSTALL_SELF -Raw; $i = $s.LastIndexOf($m); if ($i -lt 0) { [Console]::Error.WriteLine('install marker not found'); exit 1 }; Invoke-Expression $s.Substring($i)"
set "RC=%ERRORLEVEL%"

popd
pause
exit /b %RC%

#:PSSTART

$Binary  = 'Guanaco'
$Version = $null
if (Get-Command git -ErrorAction SilentlyContinue) { $Version = (git describe --tags --always --dirty 2>$null) }
if (-not $Version) { $Version = 'dev' }
$Version = $Version -replace '^v', ''
$LdFlags = "-s -w -H=windowsgui -X main.version=$Version"

$Root = (Get-Location).Path

$Palette = @{
    Info    = 'Blue'
    Success = 'Green'
    Error   = 'Red'
    Section = 'Cyan'
    Elapsed = 'Blue'
}

$Rule = [string]([char]0x2550) * 40      # U+2550 BOX DRAWINGS DOUBLE HORIZONTAL

function Write-Log {
    param(
        [Parameter(Mandatory)]
        [ValidateSet('INFO', 'SUCCESS', 'ERROR')]
        [string] $Level,
        [Parameter(Mandatory)]
        [string] $Message
    )

    switch ($Level) {
        'INFO'    { Write-Host "[INFO] $Message"    -ForegroundColor $Palette.Info }
        'SUCCESS' { Write-Host "[SUCCESS] $Message" -ForegroundColor $Palette.Success }
        'ERROR'   { Write-Host "[ERROR] $Message"   -ForegroundColor $Palette.Error }
    }
}

function Write-Info { param([string] $Message) Write-Log INFO $Message }
function Write-Ok   { param([string] $Message) Write-Log SUCCESS $Message }

function Die {
    param([string] $Message)
    Write-Log ERROR $Message
    exit 1
}

function Write-Section {
    param(
        [Parameter(Mandatory)]
        [string] $Title
    )

    Write-Host ''
    Write-Host $Rule -ForegroundColor $Palette.Section
    Write-Host "  $Title" -ForegroundColor $Palette.Section
    Write-Host $Rule -ForegroundColor $Palette.Section
}

$ErrorActionPreference = 'Stop'

try {
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
    $OutputEncoding           = [System.Text.Encoding]::UTF8
} catch { }

Write-Section "Building & Installing $Binary"

$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) {
    [Console]::Error.WriteLine('go: command not found in PATH')
    Die 'Compilation failed'
}

$running = Get-Process -Name $Binary -ErrorAction SilentlyContinue
if ($running) {
    Die "$Binary is already running - close it first (Windows locks a running .exe, so the build cannot replace it)."
}

Write-Info "Compiling $Binary..."
Write-Host ''
$clock = [System.Diagnostics.Stopwatch]::StartNew()
try {
    & $go.Source build -v "-ldflags=$LdFlags" -o "$Binary.exe" ./cmd/guanaco
    $buildExit = $LASTEXITCODE
} catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    $buildExit = 1
}
$clock.Stop()
$e = $clock.Elapsed
Write-Host ''
Write-Host ('[{0:00}:{1:00}:{2:00} elapsed]' -f [int]$e.TotalHours, $e.Minutes, $e.Seconds) -ForegroundColor $Palette.Elapsed

if ($buildExit -ne 0) {
    Die 'Compilation failed'
}

Write-Ok "Successfully compiled $Binary"

Write-Info 'Configuring desktop shortcut...'

$shortcutError = $null
$wsh = $null
try {
    $desktop  = [Environment]::GetFolderPath('Desktop')
    $exePath  = Join-Path $Root "$Binary.exe"
    $iconPath = Join-Path $Root 'assets\guanaco.ico'
    $lnkPath  = Join-Path $desktop "$Binary.lnk"

    $wsh      = New-Object -ComObject WScript.Shell
    $shortcut = $wsh.CreateShortcut($lnkPath)

    $shortcut.TargetPath       = $exePath
    $shortcut.WorkingDirectory = $Root
    # The exe carries its own icon (compiled in from assets\guanaco.ico), so point
    # the shortcut at that. Windows caches icons by file path, and a stale cache entry
    # for assets\guanaco.ico would otherwise keep showing an old picture.
    $shortcut.IconLocation = "$exePath,0"
    if (-not (Test-Path -LiteralPath $exePath) -and (Test-Path -LiteralPath $iconPath)) {
        $shortcut.IconLocation = "$iconPath,0"
    }
    $shortcut.Save()
} catch {
    $shortcutError = $_.Exception.Message
} finally {
    if ($wsh) {
        [void][System.Runtime.InteropServices.Marshal]::ReleaseComObject($wsh)
    }
}

if ($shortcutError) {
    Die "Failed to create desktop shortcut: $shortcutError"
}

Write-Section 'Setup Complete'
Write-Ok "$Binary is ready and installed on your Desktop!"
exit 0
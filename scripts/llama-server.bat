@echo off
rem Starts llama.cpp's llama-server (an OpenAI-compatible server on port 8080)
rem and then Guanaco. These are my own settings: edit the block below
rem for your GPU and models before using it.
setlocal EnableExtensions
chcp 65001 >nul
pushd "%~dp0.."
set "SELF=%~f0"
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -Command "$s = Get-Content -LiteralPath $env:SELF -Raw; $i = $s.LastIndexOf('#:PSSTART'); Invoke-Expression $s.Substring($i)"
set "RC=%ERRORLEVEL%"
popd
pause
exit /b %RC%
#:PSSTART

# ---- edit these ----
# Override with set LLAMA_CPP_DIR=... to point at a different llama.cpp checkout.
$LlamaDir = if ($env:LLAMA_CPP_DIR) { $env:LLAMA_CPP_DIR } else { (Join-Path $env:USERPROFILE 'Desktop\llama.cpp') }
$Models   = Join-Path $LlamaDir 'models'
$Port     = 8080
$Ctx      = 131072
$GpuLayers = 99
$CpuMoe   = 12          # MoE expert layers kept in RAM; raise if VRAM is full, lower for speed
$StartUI  = $true      # also launch Guanaco.exe from this folder
# --------------------

function Say($tag, $color, $msg) { Write-Host "[$tag] " -NoNewline -ForegroundColor $color; Write-Host $msg }

$exe = Join-Path $LlamaDir 'llama-server.exe'
if (-not (Test-Path -LiteralPath $exe)) { Say 'ERROR' Red "llama-server.exe not found: $exe"; exit 1 }
if (-not (Test-Path -LiteralPath $Models)) { Say 'ERROR' Red "models folder not found: $Models"; exit 1 }

$found = @(Get-ChildItem -LiteralPath $Models -Filter *.gguf -Recurse)
Say 'INFO' Blue ("{0} model(s) in {1}" -f $found.Count, $Models)
$found | ForEach-Object { Write-Host "         $($_.Name)" -ForegroundColor Cyan }
if ($found.Count -eq 0) { Say 'WARN' Yellow "no .gguf files under $Models - llama-server will start with nothing to serve" }

if ($StartUI) {
    $ui = Join-Path (Get-Location).Path 'Guanaco.exe'
    if (Test-Path -LiteralPath $ui) { Say 'INFO' Blue 'Starting Guanaco UI...'; Start-Process -FilePath $ui -WorkingDirectory (Get-Location).Path }
}

Say 'INFO' Blue "llama-server on http://127.0.0.1:$Port/v1  (Ctrl+C to stop)"
Write-Host ''
& $exe --models-dir $Models --host 127.0.0.1 --port $Port -c $Ctx -ngl $GpuLayers --n-cpu-moe $CpuMoe
exit $LASTEXITCODE

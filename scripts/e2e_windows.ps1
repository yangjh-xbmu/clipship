# clipship v0.4 file clipboard end-to-end smoke test (Windows).
#
# Prereqs:
#   - run from repo root in PowerShell
#   - go on PATH
#
# What it does:
#   1. builds clipship
#   2. starts `clipship daemon` in background
#   3. copies a known file to the OS clipboard
#   4. runs `clipship pull-file` against 127.0.0.1:19983
#   5. verifies stdout JSON + extracted file content
#   6. shuts down daemon

$ErrorActionPreference = "Stop"

Write-Host "-- build"
go build -o clipship.exe ./cmd/clipship
if (-not (Test-Path .\clipship.exe)) { throw "build failed" }

Write-Host "-- start daemon"
$daemon = Start-Process -FilePath .\clipship.exe -ArgumentList "daemon" -PassThru -WindowStyle Hidden
Start-Sleep -Milliseconds 500

try {
    Write-Host "-- make a test file and copy to clipboard"
    $testDir = Join-Path $env:TEMP "clipship-e2e-$([guid]::NewGuid())"
    New-Item -ItemType Directory -Path $testDir | Out-Null
    $srcFile = Join-Path $testDir "hello.txt"
    "hello from clipship e2e $(Get-Date -Format o)" | Set-Content -Path $srcFile -Encoding UTF8

    # Place CF_HDROP on the clipboard
    Add-Type -AssemblyName System.Windows.Forms
    $paths = New-Object System.Collections.Specialized.StringCollection
    $paths.Add($srcFile) | Out-Null
    [System.Windows.Forms.Clipboard]::SetFileDropList($paths)

    Write-Host "-- run pull-file"
    $json = & .\clipship.exe pull-file
    if ($LASTEXITCODE -ne 0) { throw "pull-file failed: exit $LASTEXITCODE" }
    Write-Host "stdout: $json"

    $parsed = $json | ConvertFrom-Json
    if ($parsed.kind -ne "file") { throw "kind = $($parsed.kind)" }
    if ($parsed.files.Count -ne 1) { throw "files count = $($parsed.files.Count)" }

    $pulled = $parsed.files[0]
    $src = Get-Content $srcFile -Raw
    $dst = Get-Content $pulled -Raw
    if ($src -ne $dst) { throw "content mismatch: src=$src dst=$dst" }

    Write-Host "-- SUCCESS"
} finally {
    Write-Host "-- stop daemon"
    Stop-Process -Id $daemon.Id -Force -ErrorAction SilentlyContinue
}

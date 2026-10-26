param(
    [switch]$SkipTest
)

$ErrorActionPreference = 'Stop'
$rootDir = $PSScriptRoot

$versionFile = Join-Path $rootDir "VERSION"
if (-not (Test-Path $versionFile)) {
    throw "VERSION 文件未找到: $versionFile"
}
$version = (Get-Content $versionFile -Raw).Trim()

if (-not $SkipTest) {
    Write-Host "Running unit tests..." -ForegroundColor Cyan
    go test ./...
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

Write-Host "Building CampusKeeper v$version..." -ForegroundColor Cyan
$binDir = Join-Path $rootDir "bin"
if (-not (Test-Path $binDir)) {
    New-Item -ItemType Directory -Path $binDir | Out-Null
}
$targetExe = Join-Path $binDir "wenchang-campus-keeper.exe"
$pkg = Join-Path $rootDir "cmd\keeper"

& go build -buildvcs=false "-ldflags=-H=windowsgui -s -w -X wenchang-campus-keeper/internal/config.AppVersion=$version" -o $targetExe $pkg

if ($LASTEXITCODE -eq 0) {
    Write-Host "Build successful: bin\wenchang-campus-keeper.exe (v$version)" -ForegroundColor Green
}

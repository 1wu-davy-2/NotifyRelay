<#
.SYNOPSIS
    Build NotifyRelay.

.DESCRIPTION
    The host Go toolchain may be 32-bit (windows/386), so the target
    architecture is pinned explicitly instead of being inherited.

.EXAMPLE
    .\build.ps1
    .\build.ps1 -Test
    .\build.ps1 -OS linux -Arch amd64 -Version 0.1.0
#>
[CmdletBinding()]
param(
    [string]$Version = "dev",
    [string]$OS = "windows",
    [string]$Arch = "amd64",
    [switch]$Test
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Push-Location $root

try {
    $env:CGO_ENABLED = "0"
    $env:GOOS = $OS
    $env:GOARCH = $Arch

    if ($Test) {
        # Bound the resource usage. A default `go test ./...` compiles every
        # package in parallel and pegs every core on the machine, which makes
        # the box unusable for anything else while it runs.
        $env:GOMAXPROCS = if ($env:GOMAXPROCS) { $env:GOMAXPROCS } else { "2" }
        $env:GOMEMLIMIT = if ($env:GOMEMLIMIT) { $env:GOMEMLIMIT } else { "1GiB" }

        Write-Host "go vet ./..."
        go vet ./...
        if ($LASTEXITCODE -ne 0) { throw "go vet failed" }

        Write-Host "go test ./... (GOMAXPROCS=$env:GOMAXPROCS GOMEMLIMIT=$env:GOMEMLIMIT)"
        go test -p 2 -parallel 2 ./...
        if ($LASTEXITCODE -ne 0) { throw "go test failed" }
    }

    $binDir = Join-Path $root "bin"
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null

    $ext = if ($OS -eq "windows") { ".exe" } else { "" }
    $out = Join-Path $binDir "notifyrelay$ext"

    Write-Host "go build -> $out ($OS/$Arch)"
    go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $out ./cmd/notifyrelay
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }

    Write-Host "built $out"
}
finally {
    Pop-Location
}

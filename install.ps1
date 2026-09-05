# install.ps1 - CHEST CLI installer for Windows
#
# Usage (PowerShell 7+):
#     irm https://raw.githubusercontent.com/Aswanidev-vs/chest/main/install.ps1 | iex
#
# Usage (PowerShell 5.1):
#     iwr https://raw.githubusercontent.com/Aswanidev-vs/chest/main/install.ps1 -UseBasicParsing | iex
#
# Parameters (when run as a file, not piped):
#     -Update        Reinstall the latest version over an existing one
#     -Uninstall     Remove an existing install
#     -To <dir>      Install into <dir> instead of the Go toolchain default
#     -NoVerify      Skip the `chest version` sanity check after install
#     -Source        Build from a local clone of the repo
#
# Exit codes:
#     0   success
#     1   generic failure
#     2   Go is not installed
#     3   Go is too old
#     4   install path is not writable
#     5   sanity check failed after install
#     6   user-supplied -To path is invalid
#
# Requires: PowerShell 5.1+ (Windows 10/11 default) or PowerShell 7+.

[CmdletBinding()]
param(
    [switch]$Update,
    [switch]$Uninstall,
    [switch]$NoVerify,
    [switch]$Source,
    [switch]$Help,
    [string]$To
)

$ErrorActionPreference = 'Continue'
$ScriptVersion = '0.1.0'
$Repo          = 'github.com/Aswanidev-vs/chest'
$Module        = "$Repo/cmd/chest@latest"
$MinGoMajor    = 1
$MinGoMinor    = 22

function Write-Banner { Write-Host '  --------------------------------------------------' -ForegroundColor DarkGray }
function Write-Info   { param([string]$M) Write-Host "  $M" }
function Write-OK     { param([string]$M) Write-Host "  + $M" -ForegroundColor Green }
function Write-Warn   { param([string]$M) Write-Host "  ! $M" -ForegroundColor Yellow }
function Write-Err    { param([string]$M) Write-Host "  X $M" -ForegroundColor Red }

function Show-Usage {
    @'
install.ps1 - CHEST CLI installer for Windows

Usage (pipe):
    irm https://raw.githubusercontent.com/Aswanidev-vs/chest/main/install.ps1 | iex

Parameters (when run as a file):
    -Update       Reinstall the latest version
    -Uninstall    Remove an existing install
    -To <dir>     Install into <dir> instead of the Go toolchain default
    -NoVerify     Skip the `chest version` sanity check
    -Source       Build from a local clone of the repo
    -Help         Show this help
'@
}

if ($Help) { Show-Usage; exit 0 }

# ---- uninstall path ----
if ($Uninstall) {
    Write-Banner
    Write-Info  "CHEST uninstaller  v$ScriptVersion"
    Write-Banner
    if ($To) {
        $BinDir = $To
    } else {
        $GoBin = $env:GOBIN
        $GoPath = (& go env GOPATH 2>$null)
        if ($GoBin)      { $BinDir = $GoBin }
        elseif ($GoPath) { $BinDir = Join-Path $GoPath 'bin' }
        else             { $BinDir = Join-Path $env:USERPROFILE 'go\bin' }
    }
    $Target = Join-Path $BinDir 'chest.exe'
    if (Test-Path -LiteralPath $Target) {
        try { Remove-Item -LiteralPath $Target -Force; Write-OK "removed $Target" }
        catch { Write-Err "could not remove $Target ($($_.Exception.Message))"; exit 4 }
    } else {
        Write-Warn "no chest.exe found at $Target"
    }
    Write-Info 'done.'
    exit 0
}

# ---- preflight: Go ----
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Banner
    Write-Err 'go is not on PATH.'
    Write-Err "Install Go $MinGoMajor.$MinGoMinor+ from https://go.dev/dl/ and re-run from a new shell."
    Write-Banner
    exit 2
}

# Parse go version
$GoVersionRaw = (& go version) -replace '^go version ',''
$GoVersion    = $GoVersionRaw -replace '^go',''
$Parts        = $GoVersion.Split('.')
$GoMajor      = if ($Parts.Length -ge 1) { [int]$Parts[0] } else { 0 }
$GoMinor      = if ($Parts.Length -ge 2) { [int]$Parts[1] } else { 0 }

if (($GoMajor -lt $MinGoMajor) -or (($GoMajor -eq $MinGoMajor) -and ($GoMinor -lt $MinGoMinor))) {
    Write-Banner
    Write-Err "Go $GoVersionRaw is too old. Need $MinGoMajor.$MinGoMinor+."
    Write-Err 'Update at https://go.dev/dl/ and re-run.'
    Write-Banner
    exit 3
}

$GoOS, $GoArch = (& go env GOOS), (& go env GOARCH)
$GoPath = (& go env GOPATH)
$GoBinSetting = (& go env GOBIN)

# ---- resolve target directory ----
if ($To) {
    if (Test-Path -LiteralPath $To) {
        if (-not (Get-Item -LiteralPath $To).PSIsContainer) {
            Write-Err "-To path exists and is not a directory: $To"
            exit 6
        }
    } else {
        try { New-Item -ItemType Directory -Force -Path $To | Out-Null }
        catch { Write-Err "cannot create -To directory: $To ($($_.Exception.Message))"; exit 6 }
    }
    $TargetDir = $To
} elseif ($GoBinSetting) {
    $TargetDir = $GoBinSetting
} elseif ($GoPath) {
    $TargetDir = Join-Path $GoPath 'bin'
} else {
    $TargetDir = Join-Path $env:USERPROFILE 'go\bin'
}

if (-not (Test-Path -LiteralPath $TargetDir)) {
    try { New-Item -ItemType Directory -Force -Path $TargetDir | Out-Null }
    catch { Write-Err "cannot create install directory: $TargetDir"; exit 4 }
}
$TestFile = Join-Path $TargetDir '.chest-write-test'
try { '' | Set-Content -LiteralPath $TestFile -ErrorAction Stop; Remove-Item -LiteralPath $TestFile -Force }
catch { Write-Err "install directory is not writable: $TargetDir"; exit 4 }

$TargetBin = Join-Path $TargetDir 'chest.exe'

# ---- header ----
Write-Banner
Write-Info  "CHEST installer  v$ScriptVersion"
Write-Info  "repo: $Repo"
Write-Banner
Write-Info  ("go                {0}" -f $GoVersionRaw)
Write-Info  ("platform          {0}/{1}" -f $GoOS, $GoArch)
Write-Info  ("install target    {0}" -f $TargetDir)
Write-Info  ("binary            {0}" -f $TargetBin)
Write-Banner

# ---- install ----
Write-Info 'installing...'

$Failed = $false
try {
    if ($Source) {
        $Work = Join-Path $env:TEMP ("chest-build-" + [Guid]::NewGuid().ToString('N').Substring(0,8))
        New-Item -ItemType Directory -Force -Path $Work | Out-Null
        $RepoDir = Join-Path $Work 'src'
        Write-Info "cloning $Repo into $RepoDir"
        git clone --depth 1 "https://$Repo.git" "$RepoDir" 2>&1 | ForEach-Object { Write-Info "  $_" }
        if ($LASTEXITCODE -ne 0) { throw "git clone failed" }
        Push-Location $RepoDir
        try {
            Write-Info 'building (this may take a minute on first run)'
            go build -o $TargetBin ./cmd/chest 2>&1 | ForEach-Object { Write-Info "  $_" }
            if ($LASTEXITCODE -ne 0) { throw "go build failed" }
        } finally { Pop-Location }
        Remove-Item -LiteralPath $Work -Recurse -Force -ErrorAction SilentlyContinue
    } else {
        Write-Info "running: go install $Module"
        $env:GOBIN = $TargetDir
        go install $Module 2>&1 | ForEach-Object { Write-Info "  $_" }
        if ($LASTEXITCODE -ne 0) { throw "go install failed" }
    }
} catch {
    Write-Err $_.Exception.Message
    $Failed = $true
}

if ($Failed) {
    exit 1
}

if (-not (Test-Path -LiteralPath $TargetBin)) {
    Write-Err "install completed but no binary was placed at $TargetBin"
    Write-Err 'if the module proxy is unreachable, try -Source and ensure git is installed'
    exit 1
}

Write-OK "installed $TargetBin"

# ---- verify ----
if (-not $NoVerify) {
    Write-Banner
    Write-Info 'verifying...'
    $VersionOut = & $TargetBin version 2>&1
    if ($LASTEXITCODE -eq 0) {
        Write-OK ("chest responds: {0}" -f ($VersionOut | Select-Object -First 1))
    } else {
        Write-Err "chest at $TargetBin did not run cleanly"
        Write-Err "try: $TargetBin version"
        exit 5
    }
}

# ---- PATH advice ----
Write-Banner
$PathDirs = $env:PATH -split ';' | ForEach-Object { $_.TrimEnd('\') }
$OnPath = $PathDirs | Where-Object { $_ -ieq $TargetDir.TrimEnd('\') } | Select-Object -First 1

if ($OnPath) {
    Write-OK "$TargetDir is on PATH -- `chest` is ready to use."
} else {
    Write-Warn "$TargetDir is NOT on PATH."
    Write-Warn 'Add it to your user PATH for this user:'
    Write-Host ''
    Write-Host "      [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ';$TargetDir', 'User')"
    Write-Host ''
    Write-Warn 'Then open a new PowerShell window.'
}

Write-Host ''
Write-Info 'quick start:'
Write-Host '      chest sort ~/Downloads -p downloads --dry-run'
Write-Host '      chest man chest'
Write-Host ''
Write-Banner
Write-Info 'done.'

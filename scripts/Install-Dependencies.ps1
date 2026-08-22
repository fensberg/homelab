#Requires -Version 5.1
<#
.SYNOPSIS
    One-time workstation setup for the homelab project. Run once, as Administrator.

.DESCRIPTION
    Installs every tool the start button (Start-Homelab.ps1) needs.

    Two toolchains are installed, because they run in different places:

      Windows native : git, opentofu, 1Password CLI, kubectl, talosctl, flux,
                       tailscale, age (encryption), rclone (R2 uploads)
      Inside WSL2    : ansible

    Ansible has no supported Windows control node, so the Ansible phase runs
    inside WSL2. Everything else runs natively in PowerShell.

    Safe to re-run. Anything already present is skipped.

.EXAMPLE
    .\scripts\Install-Dependencies.ps1

.EXAMPLE
    .\scripts\Install-Dependencies.ps1 -SkipWsl
    Use if you already have a working Ansible control node elsewhere.
#>
[CmdletBinding()]
param(
    [switch]$SkipWsl,
    [string]$ToolsDir = "$env:LOCALAPPDATA\homelab-tools"
)

$ErrorActionPreference = 'Stop'

function Write-Step { param($Message) Write-Host "`n=== $Message" -ForegroundColor Cyan }
function Write-Ok   { param($Message) Write-Host "    [ok]   $Message" -ForegroundColor Green }
function Write-Skip { param($Message) Write-Host "    [skip] $Message" -ForegroundColor DarkGray }
function Write-Warn { param($Message) Write-Host "    [warn] $Message" -ForegroundColor Yellow }

function Test-IsAdmin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    (New-Object Security.Principal.WindowsPrincipal $id).IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator)
}

if (-not (Test-IsAdmin)) {
    throw "Run this from an elevated PowerShell prompt (right-click > Run as Administrator). WSL setup needs it."
}

if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
    throw "winget not found. Install 'App Installer' from the Microsoft Store, then re-run."
}

# ---------------------------------------------------------------------------
# Windows-native tools available through winget
# ---------------------------------------------------------------------------
# Format: winget package id = the command it should put on PATH
$WingetTools = [ordered]@{
    'Git.Git'                 = 'git'
    'OpenTofu.Tofu'           = 'tofu'
    'AgileBits.1Password.CLI' = 'op'
    'Kubernetes.kubectl'      = 'kubectl'
    'Task.Task'               = 'task'
    'tailscale.tailscale'     = 'tailscale'
    'Rclone.Rclone'           = 'rclone'
}

Write-Step "Installing Windows tools via winget"
foreach ($id in $WingetTools.Keys) {
    $cmd = $WingetTools[$id]
    if (Get-Command $cmd -ErrorAction SilentlyContinue) {
        Write-Skip "$cmd already on PATH"
        continue
    }
    Write-Host "    installing $id ..."
    winget install --id $id --exact --silent --accept-source-agreements `
        --accept-package-agreements --disable-interactivity
    if ($LASTEXITCODE -ne 0) {
        Write-Warn "winget could not install '$id' (exit $LASTEXITCODE). Install it by hand and re-run."
    }
    else {
        Write-Ok "$cmd installed"
    }
}

# ---------------------------------------------------------------------------
# Tools with no winget package - pulled straight from GitHub releases
# ---------------------------------------------------------------------------
New-Item -ItemType Directory -Force -Path $ToolsDir | Out-Null

function Install-GitHubBinary {
    <#
      Downloads a single .exe asset from a GitHub release into $ToolsDir.
      Pass -Version 'latest' to resolve the newest release at run time.
    #>
    param(
        [Parameter(Mandatory)][string]$Repo,
        [Parameter(Mandatory)][string]$AssetPattern,
        [Parameter(Mandatory)][string]$ExeName,
        [switch]$FromZip
    )

    $target = Join-Path $ToolsDir $ExeName
    $stem = [IO.Path]::GetFileNameWithoutExtension($ExeName)

    if (Test-Path $target) { Write-Skip "$stem already in $ToolsDir"; return }
    if (Get-Command $stem -ErrorAction SilentlyContinue) { Write-Skip "$stem already on PATH"; return }

    $release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest" `
        -Headers @{ 'User-Agent' = 'homelab-bootstrap' }

    $asset = $release.assets | Where-Object { $_.name -like $AssetPattern } | Select-Object -First 1
    if (-not $asset) {
        Write-Warn "No asset matching '$AssetPattern' in $Repo $($release.tag_name). Install $stem manually."
        return
    }

    Write-Host "    downloading $stem $($release.tag_name) ..."

    if ($FromZip) {
        $tmpZip = Join-Path $env:TEMP "$stem.zip"
        $tmpDir = Join-Path $env:TEMP "$stem-extract"
        Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $tmpZip -UseBasicParsing
        if (Test-Path $tmpDir) { Remove-Item -Recurse -Force $tmpDir }
        Expand-Archive -Path $tmpZip -DestinationPath $tmpDir
        $found = Get-ChildItem -Path $tmpDir -Filter $ExeName -Recurse | Select-Object -First 1
        if (-not $found) { Write-Warn "$ExeName not found inside the archive."; return }
        Copy-Item $found.FullName $target -Force
        Remove-Item -Recurse -Force $tmpDir, $tmpZip -ErrorAction SilentlyContinue
    }
    else {
        Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $target -UseBasicParsing
    }

    Write-Ok "$stem -> $target"
}

Write-Step "Installing tools from GitHub releases"
Install-GitHubBinary -Repo 'siderolabs/talos' -AssetPattern 'talosctl-windows-amd64.exe' -ExeName 'talosctl.exe'
Install-GitHubBinary -Repo 'fluxcd/flux2'     -AssetPattern 'flux_*_windows_amd64.zip'   -ExeName 'flux.exe'  -FromZip
# age encrypts the state backup before it leaves the machine for Cloudflare R2.
Install-GitHubBinary -Repo 'FiloSottile/age'  -AssetPattern 'age-*-windows-amd64.zip'    -ExeName 'age.exe'   -FromZip

# Put the tools dir on the *user* PATH so new shells pick it up
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$ToolsDir*") {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$ToolsDir", 'User')
    Write-Ok "added $ToolsDir to your PATH (restart your shell to pick it up)"
}
else {
    Write-Skip "$ToolsDir already on PATH"
}
$env:Path = "$env:Path;$ToolsDir"

# ---------------------------------------------------------------------------
# WSL2 + Ansible
# ---------------------------------------------------------------------------
if ($SkipWsl) {
    Write-Step "Skipping WSL setup (-SkipWsl given)"
}
else {
    Write-Step "Setting up WSL2 + Ansible control node"

    # Reuse whatever distro is already installed rather than forcing a second
    # one; any Debian-family distro works as an Ansible control node.
    $installed = @(wsl --list --quiet 2>$null) -replace "`0", '' | Where-Object { $_ -match '\S' }

    if ($installed) {
        $distro = $installed[0]
        Write-Skip "using existing WSL distro '$distro'"
    }
    else {
        $distro = 'Debian'
        Write-Host "    installing WSL2 $distro (this may require a reboot) ..."
        wsl --install -d $distro
        Write-Warn "If Windows asks you to reboot, reboot and then re-run this script."
    }

    # Mirrored networking lets WSL share the Windows network stack, which means
    # WSL can use the Tailscale routes the Windows host already has. Without it,
    # WSL sits behind its own NAT and cannot reach 10.10.10.0/24.
    $wslConfig = Join-Path $env:USERPROFILE '.wslconfig'
    if (-not (Test-Path $wslConfig) -or -not (Select-String -Path $wslConfig -Pattern 'networkingMode' -Quiet)) {
        "`n[wsl2]`nnetworkingMode=mirrored`n" | Out-File -FilePath $wslConfig -Encoding ascii -Append
        Write-Ok "enabled mirrored networking in $wslConfig"
        Write-Warn "Run 'wsl --shutdown' before the next start so the new network mode takes effect."
    }
    else {
        Write-Skip "networkingMode already configured in .wslconfig"
    }

    Write-Host "    installing ansible inside WSL ..."
    $bootstrapWsl = @'
set -e
export DEBIAN_FRONTEND=noninteractive
sudo apt-get update -qq
sudo apt-get install -y -qq python3 python3-pip pipx openssh-client
pipx install --include-deps ansible >/dev/null 2>&1 || pipx upgrade ansible >/dev/null 2>&1 || true
pipx ensurepath >/dev/null 2>&1 || true
export PATH="$HOME/.local/bin:$PATH"
ansible-galaxy collection install ansible.posix community.general --upgrade
ansible --version | head -1
'@
    $bootstrapWsl | wsl -d $distro -- bash -s
    if ($LASTEXITCODE -ne 0) {
        Write-Warn "Ansible bootstrap inside WSL failed. Open 'wsl -d $distro' and check manually."
    }
    else {
        Write-Ok "ansible ready inside WSL"
    }
}

Write-Step "Done"
Write-Host @"

Next steps:

  1. Close and reopen PowerShell so PATH changes apply.
  2. Sign in to 1Password CLI:      op signin
  3. Confirm the vault is readable: op item list --vault homelab
  4. Preview the run:               .\scripts\Start-Homelab.ps1 -WhatIfPhase

"@ -ForegroundColor White

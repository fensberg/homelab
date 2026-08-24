#Requires -Version 5.1
<#
.SYNOPSIS
    One-time workstation setup for the homelab project. Run once, as Administrator.

.DESCRIPTION
    Installs every tool the start button (Start-Homelab.ps1) needs.

    Two toolchains are installed, because they run in different places:

      Windows native : git, opentofu, 1Password CLI, kubectl, talosctl, flux,
                       age (encryption), rclone (object storage uploads)
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
    [string]$ToolsDir = "$env:LOCALAPPDATA\homelab-tools",

    # Override if auto-detection picks the wrong distro, or picks a mangled
    # name. 'wsl --list --verbose' shows the exact strings.
    [string]$WslDistro = ''
)

$ErrorActionPreference = 'Stop'

function Write-Step { param($Message) Write-Host "`n=== $Message" -ForegroundColor Cyan }
function Write-Ok { param($Message) Write-Host "    [ok]   $Message" -ForegroundColor Green }
function Write-Skip { param($Message) Write-Host "    [skip] $Message" -ForegroundColor DarkGray }
function Write-Warn { param($Message) Write-Host "    [warn] $Message" -ForegroundColor Yellow }
function Write-Fail { param($Message) Write-Host "    [FAIL] $Message" -ForegroundColor Red }

$script:Missing = @()
$RepoRoot = Split-Path -Parent $PSScriptRoot

function Test-IsAdmin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    (New-Object Security.Principal.WindowsPrincipal $id).IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator)
}

# Elevation is only required to *install* a WSL distro. A re-run where
# everything already exists works fine unelevated, so this is a warning here
# and a hard failure later, at the point it actually matters.
$script:IsAdmin = Test-IsAdmin
if (-not $script:IsAdmin) {
    Write-Warn "Not running as Administrator. That is fine unless WSL still needs installing."
}

if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
    throw "winget not found. Install 'App Installer' from the Microsoft Store, then re-run."
}

function Update-SessionPath {
    <#
      winget writes PATH changes to the registry, not to this process. Without
      this, a tool installed a moment ago still looks missing to Get-Command.
    #>
    $machine = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    $user = [Environment]::GetEnvironmentVariable('Path', 'User')
    $env:Path = @($machine, $user, $ToolsDir) -ne '' -join ';'
}

function Test-ToolPresent {
    <#
      A tool can be installed without being on PATH - Tailscale drops its CLI
      in Program Files, for instance. Check both before deciding it is missing.
    #>
    param([string]$Command, [string[]]$ExtraPaths = @())

    if (Get-Command $Command -ErrorAction SilentlyContinue) { return $true }
    foreach ($p in $ExtraPaths) {
        if (Test-Path $p) { return $true }
    }
    return $false
}

# winget exit codes that mean "nothing to do", not "something broke".
#   0x8A15002B  update not applicable - already installed, no newer version
#   0x8A150061  package already installed
$WingetBenign = @(0, -1978335189, -1978335135)

function Install-WingetPackage {
    param(
        [Parameter(Mandatory)][string]$Id,
        [Parameter(Mandatory)][string]$Command,
        [string[]]$ExtraPaths = @(),
        [switch]$Required
    )

    if (Test-ToolPresent -Command $Command -ExtraPaths $ExtraPaths) {
        Write-Skip "$Command already present"
        return
    }

    Write-Host "    installing $Id ..."
    winget install --id $Id --exact --silent --accept-source-agreements `
        --accept-package-agreements --disable-interactivity | Out-Null
    $code = $LASTEXITCODE

    Update-SessionPath

    if ($WingetBenign -contains $code) {
        if (Test-ToolPresent -Command $Command -ExtraPaths $ExtraPaths) {
            Write-Ok "$Command ready"
        }
        else {
            # Installed according to winget, but we still cannot see it. Almost
            # always an install location that is not on PATH.
            Write-Warn "$Command is installed but not on PATH. Open a new shell, or add its folder to PATH."
        }
        return
    }

    if ($code -eq -1978335212) {
        Write-Warn "winget has no package '$Id'. Try 'winget source update', or install $Command by hand."
    }
    else {
        Write-Warn "winget could not install '$Id' (exit $code). Install $Command by hand."
    }

    if ($Required) { $script:Missing += $Command }
}

# ---------------------------------------------------------------------------
# Windows-native tools
# ---------------------------------------------------------------------------
Update-SessionPath

Write-Step "Installing required tools"
# These are the ones the start button actually invokes. Without them it stops.
Install-WingetPackage -Id 'Git.Git'                 -Command 'git'    -Required
Install-WingetPackage -Id 'OpenTofu.Tofu'           -Command 'tofu'   -Required
Install-WingetPackage -Id 'AgileBits.1Password.CLI' -Command 'op'     -Required
Install-WingetPackage -Id 'Rclone.Rclone'           -Command 'rclone' -Required

# Python is not a project dependency in itself; it is here because pre-commit
# needs it, and the repository's git hook calls pre-commit by name. Without it
# every commit either fails or gets made with the hooks bypassed, which is how
# lint errors reach a pull request unnoticed.
Install-WingetPackage -Id 'Python.Python.3.12' -Command 'python' -Required

Write-Step "Installing recommended tools"
# Useful for diagnosing the cluster by hand. Nothing in the script calls them,
# so a failure here is an inconvenience rather than a blocker.
Install-WingetPackage -Id 'Kubernetes.kubectl' -Command 'kubectl'
Install-WingetPackage -Id 'Task.Task'          -Command 'task'
Install-WingetPackage -Id 'tailscale.tailscale' -Command 'tailscale' -ExtraPaths @(
    "$env:ProgramFiles\Tailscale\tailscale.exe",
    "${env:ProgramFiles(x86)}\Tailscale\tailscale.exe"
)

# ---------------------------------------------------------------------------
# Tools with no reliable winget package - pulled from GitHub releases
# ---------------------------------------------------------------------------
New-Item -ItemType Directory -Force -Path $ToolsDir | Out-Null

function Install-GitHubBinary {
    <#
      Downloads release assets into $ToolsDir. -ExeName takes an array, because
      a single archive often ships several binaries that belong together - the
      age release contains both age.exe and age-keygen.exe, and extracting only
      the first leaves you with a broken half-install.
    #>
    param(
        [Parameter(Mandatory)][string]$Repo,
        [Parameter(Mandatory)][string]$AssetPattern,
        [Parameter(Mandatory)][string[]]$ExeName,
        [switch]$FromZip,
        [switch]$Required
    )

    $wanted = @($ExeName | Where-Object {
            -not (Test-Path (Join-Path $ToolsDir $_)) -and
            -not (Get-Command ([IO.Path]::GetFileNameWithoutExtension($_)) -ErrorAction SilentlyContinue)
        })

    if ($wanted.Count -eq 0) {
        Write-Skip "$($ExeName -join ', ') already present"
        return
    }

    try {
        $release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest" `
            -Headers @{ 'User-Agent' = 'homelab-bootstrap' }

        $asset = $release.assets | Where-Object { $_.name -like $AssetPattern } | Select-Object -First 1
        if (-not $asset) {
            Write-Warn "No asset matching '$AssetPattern' in $Repo $($release.tag_name)."
            if ($Required) { $script:Missing += $wanted }
            return
        }

        Write-Host "    downloading $($wanted -join ', ') from $Repo $($release.tag_name) ..."

        if ($FromZip) {
            $tmpZip = Join-Path $env:TEMP "gh-$($release.id).zip"
            $tmpDir = Join-Path $env:TEMP "gh-$($release.id)-extract"
            Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $tmpZip -UseBasicParsing
            if (Test-Path $tmpDir) { Remove-Item -Recurse -Force $tmpDir }
            Expand-Archive -Path $tmpZip -DestinationPath $tmpDir

            foreach ($exe in $wanted) {
                $found = Get-ChildItem -Path $tmpDir -Filter $exe -Recurse | Select-Object -First 1
                if (-not $found) {
                    Write-Warn "$exe not found inside the archive."
                    if ($Required) { $script:Missing += $exe }
                    continue
                }
                Copy-Item $found.FullName (Join-Path $ToolsDir $exe) -Force
                Write-Ok "$exe -> $ToolsDir"
            }

            Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
            Remove-Item -Force $tmpZip -ErrorAction SilentlyContinue
        }
        else {
            $exe = $wanted[0]
            Invoke-WebRequest -Uri $asset.browser_download_url -OutFile (Join-Path $ToolsDir $exe) -UseBasicParsing
            Write-Ok "$exe -> $ToolsDir"
        }
    }
    catch {
        Write-Warn "Could not install from $Repo`: $($_.Exception.Message)"
        if ($Required) { $script:Missing += $wanted }
    }
}

Write-Step "Installing tools from GitHub releases"
# age encrypts the state backup before it leaves the machine. Required.
# The age release ships the encryptor and the key generator together; the
# Backup phase needs age.exe and you need age-keygen.exe to create the key pair.
Install-GitHubBinary -Repo 'FiloSottile/age'  -AssetPattern 'age-*-windows-amd64.zip' -ExeName 'age.exe', 'age-keygen.exe' -FromZip -Required
Install-GitHubBinary -Repo 'siderolabs/talos' -AssetPattern 'talosctl-windows-amd64.exe' -ExeName 'talosctl.exe'
Install-GitHubBinary -Repo 'fluxcd/flux2'     -AssetPattern 'flux_*_windows_amd64.zip'   -ExeName 'flux.exe' -FromZip

# Put the tools dir on the *user* PATH so new shells pick it up
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$ToolsDir*") {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$ToolsDir", 'User')
    Write-Ok "added $ToolsDir to your PATH (restart your shell to pick it up)"
}
else {
    Write-Skip "$ToolsDir already on PATH"
}
Update-SessionPath

# ---------------------------------------------------------------------------
# pre-commit
# ---------------------------------------------------------------------------
Write-Step "Setting up pre-commit"

$python = (Get-Command python -ErrorAction SilentlyContinue).Source
if (-not $python) {
    Write-Warn "python not on PATH yet - open a new shell and re-run to finish pre-commit setup."
    $script:Missing += 'pre-commit'
}
else {
    # The Microsoft Store ships a python.exe stub that only offers to install
    # Python. It answers Get-Command but not --version, so check for real.
    & $python --version 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Warn "python on PATH is the Microsoft Store stub, not a real interpreter."
        Write-Warn "Disable it under Settings > Apps > Advanced app settings > App execution aliases, then re-run."
        $script:Missing += 'pre-commit'
    }
    elseif (Get-Command pre-commit -ErrorAction SilentlyContinue) {
        Write-Skip "pre-commit already present"
    }
    else {
        Write-Host "    installing pre-commit ..."
        & $python -m pip install --quiet --upgrade pip pre-commit
        if ($LASTEXITCODE -ne 0) {
            Write-Warn "pip could not install pre-commit."
            $script:Missing += 'pre-commit'
        }
        else {
            Update-SessionPath
            Write-Ok "pre-commit installed"
        }
    }
}

# Install the hook into .git/hooks so it actually runs on commit. Harmless to
# repeat, and cheap insurance against a clone that never had it.
if ((Get-Command pre-commit -ErrorAction SilentlyContinue) -and (Test-Path (Join-Path $RepoRoot '.git'))) {
    Push-Location $RepoRoot
    try {
        pre-commit install | Out-Null
        if ($LASTEXITCODE -eq 0) { Write-Ok "git pre-commit hook installed" }
        else { Write-Warn "'pre-commit install' failed - commits will not be linted." }
    }
    finally { Pop-Location }
}

# ---------------------------------------------------------------------------
# WSL2 + Ansible
# ---------------------------------------------------------------------------
function Get-WslDistro {
    <#
      Returns WSL distro names, default first.

      Do NOT parse 'wsl --list' stdout in Windows PowerShell 5.1. That command
      emits UTF-16LE, and PS 5.1 truncates the decoded native string at the
      first null byte - so "Ubuntu" arrives as "U" and no amount of
      [Console]::OutputEncoding fiddling recovers the rest.

      The registry holds the same information as plain strings. A redirect to a
      file, read back explicitly as Unicode, is the fallback.
    #>
    $root = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Lxss'

    if (Test-Path $root) {
        $defaultGuid = (Get-ItemProperty $root -Name DefaultDistribution -ErrorAction SilentlyContinue).DefaultDistribution
        $found = @()
        foreach ($key in Get-ChildItem $root -ErrorAction SilentlyContinue) {
            $name = (Get-ItemProperty $key.PSPath -Name DistributionName -ErrorAction SilentlyContinue).DistributionName
            if (-not $name) { continue }
            if ($key.PSChildName -eq $defaultGuid) { $found = @($name) + $found }
            else { $found += $name }
        }
        if ($found.Count -gt 0) { return $found }
    }

    # Fallback: let cmd redirect the UTF-16 output to a file and decode it here.
    try {
        $tmp = Join-Path $env:TEMP 'homelab-wsl-list.txt'
        cmd /c "wsl --list --quiet > `"$tmp`" 2>nul" | Out-Null
        if (Test-Path $tmp) {
            $names = @([IO.File]::ReadAllText($tmp, [Text.Encoding]::Unicode) -split "`r?`n" |
                ForEach-Object { $_.Trim() } |
                Where-Object { $_ -match '\S' })
            Remove-Item -Force $tmp -ErrorAction SilentlyContinue
            return $names
        }
    }
    catch { }

    return @()
}

if ($SkipWsl) {
    Write-Step "Skipping WSL setup (-SkipWsl given)"
}
else {
    Write-Step "Setting up WSL2 + Ansible control node"

    # @() is load-bearing: PowerShell unwraps a single-element array on return,
    # and indexing [0] into the resulting bare string yields its first CHARACTER.
    $distros = @(if ($WslDistro) { $WslDistro } else { Get-WslDistro })

    if ($distros.Count -gt 0) {
        $distro = $distros[0]

        # Prove the name is real before relying on it. Console-encoding issues
        # have historically produced truncated names here ('U' for 'Ubuntu'),
        # and the resulting failure is several steps downstream and confusing.
        & wsl -d $distro -- true 2>$null
        if ($LASTEXITCODE -ne 0) {
            Write-Fail "WSL distro name '$distro' did not respond - the name is probably truncated."
            Write-Warn "Run 'wsl --list --verbose' and re-run with the exact name:"
            Write-Warn "  .\scripts\Install-Dependencies.ps1 -WslDistro <name>"
            $script:Missing += 'ansible (WSL)'
            $distro = $null
        }
        else {
            Write-Skip "using existing WSL distro '$distro'"
        }
    }
    elseif (-not $script:IsAdmin) {
        Write-Fail "No WSL distro found, and installing one needs Administrator."
        Write-Warn "Re-run this script from an elevated PowerShell prompt."
        $script:Missing += 'ansible (WSL)'
        $distro = $null
    }
    else {
        $distro = 'Debian'
        Write-Host "    installing WSL2 $distro (this may require a reboot) ..."
        wsl --install -d $distro
        Write-Warn "If Windows asks you to reboot, reboot and then re-run this script."
        $distros = @(Get-WslDistro)
        if ($distros.Count -eq 0) {
            Write-Warn "No WSL distro available yet. Re-run after rebooting."
            $script:Missing += 'ansible (WSL)'
            $distro = $null
        }
    }

    # Mirrored networking lets WSL share the Windows network stack, so it can
    # use the overlay-network routes the Windows host already has. Without it,
    # WSL sits behind its own NAT and cannot reach the site subnets.
    $wslConfig = Join-Path $env:USERPROFILE '.wslconfig'
    if (-not (Test-Path $wslConfig) -or -not (Select-String -Path $wslConfig -Pattern 'networkingMode' -Quiet)) {
        "`n[wsl2]`nnetworkingMode=mirrored`n" | Out-File -FilePath $wslConfig -Encoding ascii -Append
        Write-Ok "enabled mirrored networking in $wslConfig"
        Write-Warn "Run 'wsl --shutdown' before the next start so the new network mode takes effect."
    }
    else {
        Write-Skip "networkingMode already configured in .wslconfig"
    }

    if ($distro) {
        # Written to a file rather than piped, so line endings are guaranteed
        # LF. A bash script with CRLF fails immediately with "\r: command not
        # found", which is a confusing way to learn about line endings.
        $bootstrap = @'
set -e

echo "--- checking outbound connectivity from WSL"
if ! curl -fsS --max-time 15 -o /dev/null https://pypi.org/simple/; then
  echo "WARN: cannot reach pypi.org from inside WSL."
  echo "      If this persists, mirrored networking may be the cause. Comment"
  echo "      out networkingMode in %USERPROFILE%\.wslconfig, run"
  echo "      'wsl --shutdown', and try again."
fi

# pip --user installs here and it is not on PATH by default, so put it in
# scope before the check - otherwise a re-run reinstalls what is already there.
export PATH="$HOME/.local/bin:$PATH"

if command -v ansible-playbook >/dev/null 2>&1; then
  echo "--- ansible already installed"
else
  echo "--- installing ansible"
  # Prefer a route that needs no root. sudo in WSL prompts for a password,
  # which turns an otherwise unattended bootstrap into a blocking prompt.
  # A modern WSL image already ships python3, pip and curl, so this is the
  # normal path and apt is only a fallback.
  if python3 -m pip --version >/dev/null 2>&1; then
    python3 -m pip install --user --quiet ansible 2>/dev/null \
      || python3 -m pip install --user --quiet --break-system-packages ansible
  else
    echo "--- pip unavailable, falling back to apt (this WILL ask for your sudo password)"
    export DEBIAN_FRONTEND=noninteractive
    sudo apt-get update -qq
    sudo apt-get install -y -qq python3-pip ansible
  fi
fi

if ! command -v ansible-playbook >/dev/null 2>&1; then
  echo "ERROR: ansible-playbook still not on PATH after install."
  echo "       Looked in: $HOME/.local/bin"
  exit 1
fi

ansible-playbook --version | head -1
ansible-galaxy collection install ansible.posix community.general --upgrade
'@

        $tmpScript = Join-Path $env:TEMP 'homelab-wsl-bootstrap.sh'
        [IO.File]::WriteAllText(
            $tmpScript,
            ($bootstrap -replace "`r`n", "`n"),
            (New-Object Text.UTF8Encoding $false))

        $wslScript = '/mnt/' + $tmpScript.Substring(0, 1).ToLower() +
                     $tmpScript.Substring(2).Replace('\', '/')

        Write-Host "    bootstrapping ansible inside '$distro' ..."
        wsl -d $distro -- bash $wslScript

        if ($LASTEXITCODE -ne 0) {
            Write-Fail "Ansible bootstrap inside WSL failed (exit $LASTEXITCODE)."
            Write-Warn "Investigate with:  wsl -d $distro"
            $script:Missing += 'ansible (WSL)'
        }
        else {
            Write-Ok "ansible ready inside '$distro'"
        }

        Remove-Item -Force $tmpScript -ErrorAction SilentlyContinue
    }
}

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
Write-Step "Summary"

if ($script:Missing.Count -gt 0) {
    Write-Fail "The start button cannot run until these are installed:"
    $script:Missing | Sort-Object -Unique | ForEach-Object { Write-Host "      - $_" -ForegroundColor Red }
    Write-Host ""
    exit 1
}

Write-Ok "everything the start button needs is present"
Write-Host @"

Next steps:

  1. Close and reopen PowerShell so PATH changes apply.
  2. Sign in to 1Password CLI:      op signin
  3. Confirm the vault is readable: op item list --vault homelab
  4. Preview the run:               .\scripts\Start-Homelab.ps1 -WhatIfPhase

"@ -ForegroundColor White

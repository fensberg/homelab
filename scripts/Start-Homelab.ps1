#Requires -Version 5.1
<#
.SYNOPSIS
    The start button. Ignites the homelab management cluster from nothing.

.DESCRIPTION
    Runs the whole ignition sequence in order:

      1 Render     - pull secrets out of 1Password into gitignored files
      2 Overlay    - apply the overlay network policy (route auto-approval), mint
                     an auth key for the hypervisor
      3 Hypervisor - run the Ansible playbook against Proxmox (via WSL)
      4 Verify     - prove the network works BEFORE spending 20 minutes on tofu
      5 Compute    - create the VMs and wait for Talos to answer
      6 Cluster    - apply Talos config, bootstrap etcd, install Flux
      7 Migrate    - move OpenTofu state from this disk into cluster Postgres
      8 Backup     - age-encrypt the state and push it off-site to Cloudflare R2
      9 Sterilize  - wipe every secret and the local state file

    SAFETY MODEL
    ------------
    The workspace is always sterilized on the way out. What differs is what
    happens to infrastructure if the run does not reach the end:

      Success        -> state lives in Postgres and R2, local copy deleted
      Failure        -> 'tofu destroy' runs FIRST, then local state is deleted,
                        so nothing is left orphaned in Proxmox
      -KeepOnFailure -> stop and keep state so you can debug. You are then
                        responsible for cleaning up.

    That ordering matters. Deleting state without destroying first would leave
    VMs running that nothing tracks any more.

.PARAMETER Phase
    Run a single phase instead of all of them. Useful while debugging.

.PARAMETER From
    Start at this phase and run everything after it.

.PARAMETER KeepOnFailure
    On error, skip the automatic destroy and keep local state for debugging.

.PARAMETER SkipUpgrade
    Tell the playbook not to run a full apt dist-upgrade on the hypervisor.

.EXAMPLE
    .\scripts\Start-Homelab.ps1
    The whole sequence.

.EXAMPLE
    .\scripts\Start-Homelab.ps1 -Phase Verify
    Just check whether the network is healthy.

.EXAMPLE
    .\scripts\Start-Homelab.ps1 -From Compute -KeepOnFailure
    Resume after the hypervisor is configured, leaving the mess intact on error.
#>
[CmdletBinding()]
param(
    [ValidateSet('Render', 'Overlay', 'Hypervisor', 'Verify', 'Compute', 'Cluster', 'Migrate', 'Backup', 'Sterilize')]
    [string]$Phase,

    [ValidateSet('Render', 'Overlay', 'Hypervisor', 'Verify', 'Compute', 'Cluster', 'Migrate', 'Backup', 'Sterilize')]
    [string]$From,

    # Which key in the config's sites map to deploy. Selection only - the
    # site's identity comes from the octet it declares.
    [string]$Site = 'site0',

    # Re-resolve providers against the version constraints instead of the
    # committed lock file. Needed only after a constraint changes; the new
    # lock must then be committed.
    [switch]$Upgrade,

    [switch]$KeepOnFailure,
    [switch]$SkipUpgrade,
    [switch]$WhatIfPhase,

    # Leave empty to use whichever distro WSL has set as default.
    [string]$WslDistro = ''
)

$ErrorActionPreference = 'Stop'

# --- paths -----------------------------------------------------------------
$RepoRoot       = Split-Path -Parent $PSScriptRoot
$ConfigTpl      = Join-Path $RepoRoot 'config\management.tpl.json'
$ConfigRendered = Join-Path $RepoRoot 'config\management.rendered.json'
$HypervisorDir  = Join-Path $RepoRoot 'management\hypervisor'
$InventoryOut   = Join-Path $HypervisorDir 'inventory.yml'
$OverlayVars    = Join-Path $HypervisorDir 'overlay-network.auto.yml'
$SiteVars       = Join-Path $HypervisorDir 'site.auto.yml'
$ClusterDir     = Join-Path $RepoRoot 'management\cluster'
$BackendPgOff   = Join-Path $ClusterDir 'backend_pg.tf.disabled'
$BackendPgOn    = Join-Path $ClusterDir 'backend_pg.tf'
$LocalState     = Join-Path $ClusterDir 'terraform.tfstate'

$AllPhases = @('Render', 'Overlay', 'Hypervisor', 'Verify', 'Compute', 'Cluster', 'Migrate', 'Backup', 'Sterilize')

# Addressing follows the octet declared on each site. Two sites sharing one
# collide on the overlay network and present as a broken network rather than a
# config mistake, so uniqueness is checked here and again by registry.tf at
# plan time - this copy just fails in a second rather than after a provider
# round trip.
function Get-SiteNetwork {
    param([Parameter(Mandatory)][string]$Name)

    $cfg = Read-RenderedConfig
    if (-not $cfg.sites) { throw "The config defines no sites." }

    $known = @($cfg.sites.PSObject.Properties.Name)
    if ($known -notcontains $Name) {
        throw "Unknown site '$Name'. The config defines: $(($known | Sort-Object) -join ', ')"
    }
    $site = $cfg.sites.$Name

    # Octets are declared, so uniqueness has to be checked rather than assumed.
    # Checked across every site, not just the selected one - a collision the
    # other way round is just as broken.
    $octets = @($known | ForEach-Object { $cfg.sites.$_.octet })
    $dupes = @($octets | Group-Object | Where-Object Count -gt 1 | ForEach-Object { $_.Name })
    if ($dupes.Count -gt 0) {
        throw "Duplicate octet(s) in sites: $($dupes -join ', '). Each site owns 10.<octet>.0.0/16; two sites sharing one collide on the overlay network."
    }
    $outOfRange = @($octets | Where-Object { $_ -lt 1 -or $_ -gt 95 })
    if ($outOfRange.Count -gt 0) {
        throw "Octet(s) out of range: $($outOfRange -join ', '). Use 1-95; Kubernetes defaults occupy 10.96.0.0/12 and 10.244.0.0/16."
    }

    # Vendor lock, checked the way that actually matters: the vault attests
    # what its credentials are, and it must agree with what the config
    # declares. Comparing the config against the code alone would compare two
    # files that always change together.
    foreach ($concern in 'hypervisor', 'overlay_network', 'object_storage') {
        $declared = $site.$concern.provider
        $attested = $site.$concern.vault_provider
        if ([string]::IsNullOrWhiteSpace($attested)) {
            throw "sites.$Name.$concern has no vault_provider. The 1Password item must attest which vendor its credentials belong to."
        }
        if ($declared -ne $attested) {
            throw "Vendor mismatch in sites.$Name.$concern - the config declares '$declared' but the vault item attests '$attested'. Either the wrong item is referenced, or its credentials were replaced without updating its provider field."
        }
    }

    # A declaration only catches someone who updates the declaration. AKIA and
    # ASIA prefixes are AWS long-term and temporary credentials; R2 issues 32
    # hex characters, so this is positive identification, not a heuristic.
    if ($site.object_storage.access_key_id -match '^(AKIA|ASIA)') {
        throw "sites.$Name.object_storage.access_key_id is an AWS credential (AKIA/ASIA prefix) but this site declares $($site.object_storage.provider)."
    }

    # nodes is a map; sort by key so placement matches OpenTofu's ordering.
    $nodes = @($site.hypervisor.nodes.PSObject.Properties | Sort-Object Name | ForEach-Object { $_.Value })
    if ($nodes.Count -eq 0) { throw "Site '$Name' has no hypervisor nodes." }
    if ($site.control_plane_count -lt 1) { throw "Site '$Name' has control_plane_count $($site.control_plane_count); it must be at least 1." }

    $o = $site.octet

    # Must match the site_name expression in variables.tf: lowercase, every run
    # of non-alphanumerics collapsed to a hyphen, trimmed. These become Proxmox
    # VM names, so "Sheridan Road Office" has to become "sheridan-road-office".
    $slug = ($site.name -replace '[^A-Za-z0-9]+', '-').Trim('-').ToLower()
    if ([string]::IsNullOrWhiteSpace($slug)) { $slug = $Name }
    $label = if ([string]::IsNullOrWhiteSpace($site.name)) { $slug } else { $site.name }

    [pscustomobject]@{
        Name        = $slug
        Key         = $Name
        Label       = $label
        SiteCidr    = "10.$o.0.0/16"
        NodeCidr    = "10.$o.10.0/24"
        Gateway     = "10.$o.10.1"
        DhcpStart   = "10.$o.10.50"
        DhcpEnd     = "10.$o.10.99"
        NodeIps     = @(0..($site.control_plane_count - 1) | ForEach-Object { "10.$o.10.$(100 + $_)" })
        VmNames     = @(0..($site.control_plane_count - 1) | ForEach-Object { "$slug-cp-{0:d2}" -f ($_ + 1) })
        Hypervisors = $nodes
    }
}

# OpenTofu reads the same config; this tells it which site to use.
$env:TF_VAR_site = $Site

# --- output helpers --------------------------------------------------------
$script:PhaseNum = 0
function Write-Phase {
    param($Name, $Description)
    $script:PhaseNum++
    Write-Host ""
    Write-Host ("=" * 72) -ForegroundColor DarkCyan
    Write-Host (" PHASE {0} : {1}" -f $script:PhaseNum, $Name.ToUpper()) -ForegroundColor Cyan
    Write-Host (" {0}" -f $Description) -ForegroundColor DarkGray
    Write-Host ("=" * 72) -ForegroundColor DarkCyan
}
function Write-Info { param($m) Write-Host "  -> $m" -ForegroundColor White }
function Write-Ok { param($m) Write-Host "  [ok] $m" -ForegroundColor Green }
function Write-Warn { param($m) Write-Host "  [!!] $m" -ForegroundColor Yellow }

function Invoke-Native {
    <#
      Runs an external command and throws if it fails. PowerShell will not do
      this for you: $ErrorActionPreference does not apply to native exit codes.
    #>
    param([Parameter(Mandatory)][scriptblock]$Command, [string]$What = 'command')
    & $Command
    if ($LASTEXITCODE -ne 0) { throw "$What failed with exit code $LASTEXITCODE" }
}

function Invoke-Tofu {
    <#
      Arguments MUST be splatted from an array. Windows PowerShell 5.1 splits a
      bare token like -target=type.name at the dot, so tofu receives
      "-target=type" and ".name" as two arguments and rejects the target as
      incomplete. Splatting passes each element through untouched.
    #>
    param(
        [Parameter(Mandatory)][string[]]$Arguments,
        [string]$What = 'tofu'
    )
    & tofu @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$What failed with exit code $LASTEXITCODE" }
}

function Invoke-TofuInit {
    <#
      Plain init by default, so the committed .terraform.lock.hcl decides the
      provider versions and every machine gets the same ones. -Upgrade
      re-resolves against the constraints, which is only correct right after a
      constraint changes.
    #>
    $initArgs = @('init', '-input=false')
    if ($Upgrade) { $initArgs += '-upgrade' }
    & tofu @initArgs
    if ($LASTEXITCODE -eq 0) { return }

    if (-not $Upgrade) {
        throw @"
tofu init failed with exit code $LASTEXITCODE.

If it complained that a locked provider does not match its version
constraint, the committed lock file is behind management/cluster/versions.tf.
Re-resolve and commit the result:

    .\scripts\Start-Homelab.ps1 -Phase Overlay -Upgrade
    git add management/cluster/.terraform.lock.hcl && git commit

"@
    }
    throw "tofu init -upgrade failed with exit code $LASTEXITCODE"
}

function Read-RenderedConfig {
    if (-not (Test-Path $ConfigRendered)) {
        throw "Rendered config not found. Run the Render phase first."
    }
    Get-Content $ConfigRendered -Raw | ConvertFrom-Json
}

function Get-JsonLeaf {
    <# Flattens a parsed JSON tree to dotted paths and their scalar values. #>
    param($Node, [string]$Path = '')

    if ($null -eq $Node) {
        return [pscustomobject]@{ Path = $Path; Value = $null }
    }
    if ($Node -is [System.Management.Automation.PSCustomObject]) {
        foreach ($prop in $Node.PSObject.Properties) {
            $child = if ($Path) { "$Path.$($prop.Name)" } else { $prop.Name }
            Get-JsonLeaf -Node $prop.Value -Path $child
        }
        return
    }
    if ($Node -is [System.Collections.IEnumerable] -and $Node -isnot [string]) {
        $i = 0
        foreach ($item in $Node) { Get-JsonLeaf -Node $item -Path "$Path[$i]"; $i++ }
        return
    }
    [pscustomobject]@{ Path = $Path; Value = $Node }
}

function Assert-RenderedConfigComplete {
    <#
      Every op:// reference in the template must resolve to a real value.

      A blank 1Password field resolves to an empty string, which op inject
      reports as success. The empty value then travels all the way into a
      provider, where it surfaces as something like "credentials are empty"
      with no indication of which vault field is at fault. Comparing the
      template against the rendered output names the exact reference.
    #>
    $template = Get-Content $ConfigTpl -Raw | ConvertFrom-Json
    $rendered = Read-RenderedConfig

    $renderedByPath = @{}
    foreach ($leaf in (Get-JsonLeaf -Node $rendered)) { $renderedByPath[$leaf.Path] = $leaf.Value }

    $empty = @()
    $unresolved = @()

    foreach ($leaf in (Get-JsonLeaf -Node $template)) {
        if ($leaf.Value -isnot [string]) { continue }
        if ($leaf.Value -notmatch 'op://') { continue }

        $reference = ([regex]::Match($leaf.Value, 'op://[^\s}]+')).Value
        $actual = $renderedByPath[$leaf.Path]

        if ($actual -is [string] -and $actual -match 'op://') {
            $unresolved += "  $($leaf.Path)  <-  $reference"
        }
        elseif ([string]::IsNullOrWhiteSpace($actual)) {
            $empty += "  $($leaf.Path)  <-  $reference"
        }
    }

    if ($unresolved.Count -gt 0) {
        throw @"
op inject did not substitute $($unresolved.Count) reference(s):

$($unresolved -join "`n")

The item or field does not exist, or the path is misspelled. Check with:
    op read "<the reference above>"
"@
    }

    if ($empty.Count -gt 0) {
        throw @"
$($empty.Count) vault field(s) resolved to an empty value:

$($empty -join "`n")

The field exists but has no content. Fill it in, or remove the entry from
config/management.tpl.json if this deployment does not need it. Empty values
would otherwise reach a provider and fail as "credentials are empty", which
does not say which field is missing.
"@
    }
}

function Convert-ToWslPath {
    param([Parameter(Mandatory)][string]$WindowsPath)
    $full = (Resolve-Path $WindowsPath).Path
    $drive = $full.Substring(0, 1).ToLower()
    '/mnt/' + $drive + $full.Substring(2).Replace('\', '/')
}

function Test-Port {
    param([string]$ComputerName, [int]$Port)
    $t = Test-NetConnection -ComputerName $ComputerName -Port $Port -WarningAction SilentlyContinue
    return $t.TcpTestSucceeded
}

function Wait-ForPort {
    <# Polls a TCP port until it opens or the timeout expires. #>
    param(
        [string]$ComputerName,
        [int]$Port,
        [int]$TimeoutMinutes = 5,
        [int]$IntervalSeconds = 10
    )
    $deadline = (Get-Date).AddMinutes($TimeoutMinutes)
    while ((Get-Date) -lt $deadline) {
        if (Test-Port -ComputerName $ComputerName -Port $Port) { return $true }
        Start-Sleep -Seconds $IntervalSeconds
    }
    return $false
}

# ===========================================================================
# PHASE 1 - Render
# ===========================================================================
function Invoke-PhaseRender {
    Write-Phase 'Render' 'Pull secrets from 1Password into gitignored files.'

    if (-not (Get-Command op -ErrorAction SilentlyContinue)) {
        throw "1Password CLI ('op') not found. Run scripts\Install-Dependencies.ps1."
    }

    # Sign in first rather than failing and making the operator do it. An
    # unsigned CLI would otherwise surface as 'op inject' emitting a
    # half-rendered file, which is a far worse failure than a prompt.
    & op whoami 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Info "not signed in to 1Password - starting sign-in"
        & op signin
        & op whoami 2>&1 | Out-Null
        if ($LASTEXITCODE -ne 0) {
            throw @"
Still not signed in to 1Password after attempting sign-in.

If the desktop app is installed, enable Settings > Developer > Integrate with
1Password CLI, unlock the app, and re-run. Otherwise sign in manually:

    op signin

"@
        }
    }
    $account = (& op whoami --format=json 2>$null | ConvertFrom-Json)
    if ($account) { Write-Ok "signed in to 1Password as $($account.email)" }

    Write-Info "rendering config\management.rendered.json"
    # op inject prints the output path on success; it is noise here.
    Invoke-Native { op inject -i $ConfigTpl -o $ConfigRendered -f | Out-Null } '1Password inject (config)'

    # The inventory is generated, not templated: op inject substitutes into a
    # fixed file and cannot loop over sites[].hypervisor.nodes. Generating it is
    # what makes appending a node genuinely sufficient.
    $Net = Get-SiteNetwork -Name $Site

    Write-Info "generating inventory for $($Net.Name) ($($Net.Hypervisors.Count) hypervisor(s))"
    $inv = @("---", "all:", "  children:", "    hypervisors:", "      hosts:")
    foreach ($h in $Net.Hypervisors) {
        $inv += "        `"$($h.hostname)`":"
        $inv += "          ansible_host: `"$($h.ip)`""
        $inv += "          ansible_user: root"
    }
    ($inv -join "`n") + "`n" | Out-File -FilePath $InventoryOut -Encoding ascii -NoNewline

    # Not a secret, but it lives beside the rendered files and is wiped with
    # them, so the playbook only ever sees one site's values.
    Write-Info "writing per-site network values for Ansible"
    @(
        "---",
        "sdn_subnet: `"$($Net.NodeCidr)`"",
        "sdn_gateway: `"$($Net.Gateway)`"",
        "sdn_dhcp_start: `"$($Net.DhcpStart)`"",
        "sdn_dhcp_end: `"$($Net.DhcpEnd)`"",
        "advertise_routes: `"$($Net.SiteCidr)`""
    ) -join "`n" | Out-File -FilePath $SiteVars -Encoding ascii

    Assert-RenderedConfigComplete
    Write-Ok "secrets rendered"
}

# ===========================================================================
# PHASE 2 - Overlay
# ===========================================================================
function Invoke-PhaseOverlay {
    Write-Phase 'Overlay' 'Mint a tagged auth key for the hypervisor to join the overlay network.'

    Push-Location $ClusterDir
    try {
        Write-Info "tofu init"
        Invoke-TofuInit

        # Applied ahead of the playbook so the hypervisor can log in with a
        # tagged key. The tag is what makes autoApprovers approve the subnet
        # route without anyone touching the admin console.
        Write-Info "minting a tagged auth key"
        Invoke-Tofu @(
            'apply', '-input=false', '-auto-approve',
            '-target=tailscale_tailnet_key.hypervisor'
        ) 'tofu apply (overlay network)'

        $key = & tofu output -raw overlay_network_auth_key
        if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($key)) {
            throw "Could not read the overlay_network_auth_key output."
        }

        # Handed to Ansible as a vars file rather than on the command line,
        # where it would be visible in the process list. Sterilize deletes it.
        Write-Info "writing the auth key for Ansible"
        "---`noverlay_auth_key: `"$key`"`n" |
            Out-File -FilePath $OverlayVars -Encoding ascii -NoNewline

        Write-Ok "auth key minted; the tailnet policy auto-approves this subnet"
    }
    finally {
        Pop-Location
    }
}

# ===========================================================================
# PHASE 3 - Hypervisor
# ===========================================================================
function Invoke-PhaseHypervisor {
    Write-Phase 'Hypervisor' 'Configure Proxmox: repos, Tailscale, RBAC, SDN. Safe to re-run.'

    $wslRepo = Convert-ToWslPath $HypervisorDir
    $extraVars = if ($SkipUpgrade) { "-e do_dist_upgrade=false" } else { "" }
    $keyVars = if (Test-Path $OverlayVars) { "-e @overlay-network.auto.yml" } else { "" }
    if (-not (Test-Path $SiteVars)) {
        throw "No site.auto.yml - run the Render phase first so the playbook knows this site's network."
    }
    $siteVars = "-e @site.auto.yml"

    if (-not (Test-Path $OverlayVars)) {
        Write-Warn "No overlay-network.auto.yml - run the Overlay phase first, or log the host in by hand."
    }

    # Ansible cannot run natively on Windows, so this phase hops into WSL.
    # The repo is read from /mnt/c/... - slower than a native FS, fine here.
    #
    # Written to a file rather than passed as bash -lc "...". WSL inherits the
    # Windows PATH, which contains "Program Files (x86)", and an unquoted
    # assignment of it is a bash syntax error at the parenthesis. A script file
    # sidesteps every layer of quoting between PowerShell, wsl.exe and bash.
    # Resolved here rather than inherited: PowerShell scopes variables to the
    # function that set them, so $Net from the Render phase is not visible in
    # this one. Reading it as if it were left $hostList empty, which made the
    # preflight loop below iterate zero times and silently do nothing.
    $Net = Get-SiteNetwork -Name $Site
    $hostList = ($Net.Hypervisors | ForEach-Object { $_.ip }) -join ' '
    if ([string]::IsNullOrWhiteSpace($hostList)) {
        throw "No hypervisor addresses resolved for site '$Site'. The preflight cannot run, so refusing to continue."
    }

    # ansible.cfg is NOT picked up from this directory: /mnt/c is world-writable
    # under WSL, and Ansible refuses to load a config file from a world-writable
    # directory. Naming the file explicitly through ANSIBLE_CONFIG is honoured,
    # because that is a deliberate choice rather than ambient discovery.
    $wslAnsibleCfg = Convert-ToWslPath (Join-Path $HypervisorDir 'ansible.cfg')

    $script = @"
set -e
export PATH="`$HOME/.local/bin:`$PATH"
export ANSIBLE_CONFIG='$wslAnsibleCfg'
cd '$wslRepo'

# Prove we can log in before handing over to Ansible. Ansible reports an SSH
# failure as an UNREACHABLE task, which buries the actual cause - a missing
# host key, or no usable credential - under a play recap.
for h in $hostList; do
  if ! ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 "root@`$h" true 2>/tmp/homelab-ssh.err; then
    echo "ERROR: cannot log in to root@`$h from WSL without a password." >&2
    echo >&2
    sed 's/^/  ssh: /' /tmp/homelab-ssh.err >&2
    echo >&2
    echo "  Install this machine's key on the hypervisor, once:" >&2
    echo >&2
    echo "      wsl ssh-copy-id root@`$h" >&2
    echo >&2
    echo "  It asks for the Proxmox root password that one time. Every run" >&2
    echo "  after it is key-based and needs no password at all." >&2
    rm -f /tmp/homelab-ssh.err
    exit 1
  fi
done
rm -f /tmp/homelab-ssh.err

exec ansible-playbook -i inventory.yml hypervisor-prep.yml $siteVars $keyVars $extraVars
"@

    $tmpScript = Join-Path $env:TEMP 'homelab-hypervisor.sh'
    [IO.File]::WriteAllText(
        $tmpScript,
        ($script -replace "`r`n", "`n"),
        (New-Object Text.UTF8Encoding $false))
    $wslScript = Convert-ToWslPath $tmpScript

    # With no -d, WSL uses the default distro, whatever it happens to be.
    $wslArgs = if ($WslDistro) { @('-d', $WslDistro, '--', 'bash', $wslScript) }
               else { @('--', 'bash', $wslScript) }

    Write-Info ("running the playbook inside WSL" + $(if ($WslDistro) { " ($WslDistro)" } else { " (default distro)" }))
    try {
        Invoke-Native { wsl @wslArgs } 'ansible-playbook'
    }
    finally {
        Remove-Item -Force $tmpScript -ErrorAction SilentlyContinue
    }

    Write-Ok "hypervisor configured"
}

# ===========================================================================
# PHASE 4 - Verify
# ===========================================================================
function Invoke-PhaseVerify {
    Write-Phase 'Verify' 'Prove the network works before spending time on OpenTofu.'

    $Net = Get-SiteNetwork -Name $Site
    $SdnGateway = $Net.Gateway
    $pveHost = $Net.Hypervisors[0].ip

    Write-Info "checking the Proxmox API on $pveHost ..."
    if (-not (Test-Port -ComputerName $pveHost -Port 8006)) {
        throw "Cannot reach the Proxmox API at ${pveHost}:8006. Fix that before continuing."
    }
    Write-Ok "Proxmox API reachable"

    Write-Info "checking the SDN gateway at $SdnGateway ..."
    if (-not (Test-Connection -ComputerName $SdnGateway -Count 2 -Quiet -ErrorAction SilentlyContinue)) {
        Write-Warn "No reply from $SdnGateway."
        Write-Host @"

  This is the single most common reason ignition hangs. Two usual causes:

    a) The Proxmox SDN was never applied to the kernel.
       On the Proxmox host, run:  ip -br addr show vnetint
       You want to see it UP with $($Net.Gateway)/24.

    b) The Tailscale subnet route is not active.
       The Overlay phase should have auto-approved it. Check with:
         tailscale status --json
       and confirm the hypervisor logged in with the tagged auth key.

"@ -ForegroundColor Yellow
        throw "SDN gateway $SdnGateway is unreachable - stopping before OpenTofu."
    }
    Write-Ok "SDN gateway reachable - the path to your future nodes works"
}

# ===========================================================================
# PHASE 5 - Compute
# ===========================================================================
function Invoke-PhaseCompute {
    Write-Phase 'Compute' 'Create the Talos VMs, then wait for them to answer.'

    Push-Location $ClusterDir
    try {
        Write-Info "tofu init"
        Invoke-TofuInit

        # Build the VMs only. Splitting this from the Talos phase means a
        # failure here is obviously a Proxmox problem, not a Talos one.
        Write-Info "creating the ISO and the virtual machines"
        Invoke-Tofu @(
            'apply', '-input=false', '-auto-approve',
            '-target=proxmox_virtual_environment_file.talos_iso',
            '-target=proxmox_virtual_environment_vm.talos_cp'
        ) 'tofu apply (compute)'

        Write-Ok "VMs created"

        # The provider returns as soon as Proxmox defines the VM. Talos still
        # has to boot. Poll its API port rather than guessing at a sleep.
        foreach ($node in (Get-SiteNetwork -Name $Site).NodeIps) {
            Write-Info "waiting for the Talos API on ${node}:50000 ..."
            if (-not (Wait-ForPort -ComputerName $node -Port 50000 -TimeoutMinutes 5)) {
                throw @"
Talos on $node never came up within 5 minutes.

Open that VM's console in the Proxmox web UI. Talos prints its IP on the
maintenance-mode banner:

  - No IP shown    -> the SDN bridge or DHCP is wrong (see the Verify phase).
  - A different IP -> cloud-init did not apply your static address.
  - The correct IP -> the VM is fine and this is a routing problem.
"@
            }
            Write-Ok "$node is up in maintenance mode"
        }
    }
    finally {
        Pop-Location
    }
}

# ===========================================================================
# PHASE 6 - Cluster
# ===========================================================================
function Invoke-PhaseCluster {
    Write-Phase 'Cluster' 'Apply Talos config, bootstrap etcd, install Flux.'

    Push-Location $ClusterDir
    try {
        Write-Info "applying the Talos machine configuration"
        Invoke-Tofu @(
            'apply', '-input=false', '-auto-approve',
            '-target=talos_machine_configuration_apply.control_plane'
        ) 'tofu apply (talos config)'

        Write-Info "bootstrapping etcd"
        Invoke-Tofu @(
            'apply', '-input=false', '-auto-approve',
            '-target=talos_machine_bootstrap.this'
        ) 'tofu apply (bootstrap)'

        # Everything else, including the Flux bootstrap. Flux goes last because
        # its provider is configured from the kubeconfig the previous steps
        # produce.
        Write-Info "installing Flux and finishing the apply"
        Invoke-Tofu @('apply', '-input=false', '-auto-approve') 'tofu apply (flux)'

        Write-Ok "cluster is up and Flux is reconciling"
    }
    finally {
        Pop-Location
    }
}

# ===========================================================================
# PHASE 7 - Migrate
# ===========================================================================
function Invoke-PhaseMigrate {
    Write-Phase 'Migrate' 'Move OpenTofu state off this disk and into cluster Postgres.'

    Push-Location $ClusterDir
    try {
        # Derived from variables.tf plus the 1Password password, not stored as
        # a secret of its own - see the state_conn_str output in database.tf.
        # Storing it would invent a chicken-and-egg problem: you cannot record
        # a connection string for a database that does not exist yet.
        $connStr = & tofu output -raw state_conn_str
        if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($connStr)) {
            throw "Could not read the state_conn_str output. Has the Cluster phase run?"
        }

        if ($connStr -match '@([^:/@]+):(\d+)') {
            $pgHost = $Matches[1]; $pgPort = [int]$Matches[2]
        }
        else {
            throw "Could not parse a host and port out of the derived connection string."
        }

        Write-Info "waiting for Postgres at ${pgHost}:${pgPort} ..."
        if (-not (Wait-ForPort -ComputerName $pgHost -Port $pgPort -TimeoutMinutes 10 -IntervalSeconds 15)) {
            throw "Postgres at ${pgHost}:${pgPort} never became reachable. Has Flux finished reconciling it?"
        }
        Write-Ok "Postgres reachable"

        # Turn the backend on by renaming the file in. It stays '.disabled' in
        # git so a fresh clone always starts on local state.
        Write-Info "enabling the Postgres backend"
        Copy-Item $BackendPgOff $BackendPgOn -Force

        Write-Info "migrating state (local -> Postgres)"
        Invoke-Tofu @(
            'init', '-input=false', '-migrate-state', '-force-copy',
            "-backend-config=conn_str=$connStr"
        ) 'tofu init -migrate-state'

        Write-Ok "state now lives in Postgres"
    }
    finally {
        Pop-Location
    }
}

# ===========================================================================
# PHASE 8 - Backup
# ===========================================================================
function Invoke-PhaseBackup {
    Write-Phase 'Backup' 'Encrypt the state and push it off-site to Cloudflare R2.'

    $cfg = Read-RenderedConfig
    $site = $cfg.sites.$Site
    $store = $site.object_storage

    foreach ($field in 'account_id', 'access_key_id', 'secret_access_key', 'bucket') {
        if ([string]::IsNullOrWhiteSpace($store.$field)) {
            throw "sites.$Site.object_storage.$field is missing from the rendered config."
        }
    }
    if ([string]::IsNullOrWhiteSpace($site.state.backup_recipient)) {
        throw @"
No 'state.backup_recipient' for site $Site in the rendered config.

State is never uploaded in plaintext. Generate a key pair once:

    age-keygen -o state-backup.key

Put the PUBLIC recipient (the 'age1...' line) in 1Password at
op://homelab/site<N>-state-database/backup_recipient, and store the private key file
somewhere offline. The automation only ever needs the public half - it can
write backups but cannot read them back.
"@
    }

    foreach ($tool in 'age', 'rclone') {
        if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
            throw "'$tool' not found. Run scripts\Install-Dependencies.ps1."
        }
    }

    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $tmpPlain = Join-Path $env:TEMP "tofu-state-$stamp.json"
    $tmpCipher = "$tmpPlain.age"

    Push-Location $ClusterDir
    try {
        # Pull from whichever backend is currently authoritative. After the
        # Migrate phase that is Postgres, which is exactly what we want to back
        # up - the local file is already gone by then.
        Write-Info "pulling the current state"
        & tofu state pull | Out-File -FilePath $tmpPlain -Encoding utf8
        if ($LASTEXITCODE -ne 0) { throw "tofu state pull failed with exit code $LASTEXITCODE" }

        $size = (Get-Item $tmpPlain).Length
        if ($size -lt 100) { throw "State pull returned only $size bytes - refusing to upload it." }

        Write-Info "encrypting to $($site.state.backup_recipient.Substring(0, [Math]::Min(20, $site.state.backup_recipient.Length)))..."
        Invoke-Native { age --recipient $site.state.backup_recipient --output $tmpCipher $tmpPlain } 'age encrypt'

        # rclone is configured entirely through environment variables so no
        # credentials are ever written to a config file on disk.
        $env:RCLONE_CONFIG_R2_TYPE = 's3'
        $env:RCLONE_CONFIG_R2_PROVIDER = 'Cloudflare'
        $env:RCLONE_CONFIG_R2_ACCESS_KEY_ID = $store.access_key_id
        $env:RCLONE_CONFIG_R2_SECRET_ACCESS_KEY = $store.secret_access_key
        $env:RCLONE_CONFIG_R2_ENDPOINT = "https://$($store.account_id).r2.cloudflarestorage.com"
        $env:RCLONE_CONFIG_R2_NO_CHECK_BUCKET = 'true'

        try {
            # A timestamped copy for history, plus a stable 'latest' pointer.
            $dest = "R2:$($store.bucket)/management-cluster"
            Write-Info "uploading to $dest/$stamp.tfstate.age"
            Invoke-Native { rclone copyto $tmpCipher "$dest/$stamp.tfstate.age" } 'rclone upload (timestamped)'

            Write-Info "updating $dest/latest.tfstate.age"
            Invoke-Native { rclone copyto $tmpCipher "$dest/latest.tfstate.age" } 'rclone upload (latest)'
        }
        finally {
            Remove-Item Env:\RCLONE_CONFIG_R2_* -ErrorAction SilentlyContinue
        }

        Write-Ok "encrypted state backed up to Cloudflare R2"

        # The private identity is deliberately absent from the config contract:
        # this script can write backups and must not be able to read them. It
        # is fetched by a human, by hand, only when restoring.
        Write-Host @"

  To restore, on a machine with op signed in:

    op read "op://homelab/$Site/state-database/backup_identity" > `$env:TEMP/restore.key
    rclone cat R2:$($store.bucket)/management-cluster/latest.tfstate.age |
        age -d -i `$env:TEMP/restore.key > terraform.tfstate
    Remove-Item `$env:TEMP/restore.key

"@ -ForegroundColor DarkGray
    }
    finally {
        Pop-Location
        # The plaintext state is the most sensitive artefact this script ever
        # touches. Remove it whatever happened above.
        Remove-Item -Force $tmpPlain, $tmpCipher -ErrorAction SilentlyContinue
    }
}

# ===========================================================================
# PHASE 9 - Sterilize
# ===========================================================================
function Invoke-PhaseSterilize {
    param([switch]$Quiet)
    if (-not $Quiet) {
        Write-Phase 'Sterilize' 'Remove every secret and the local state file from this workstation.'
    }

    $targets = @(
        $ConfigRendered,
        $InventoryOut,
        $OverlayVars,
        $SiteVars,
        $BackendPgOn,
        $LocalState,
        "$LocalState.backup"
    )

    foreach ($t in $targets) {
        if (Test-Path $t) {
            Remove-Item -Force $t
            Write-Info "removed $(Split-Path -Leaf $t)"
        }
    }
    Write-Ok "workspace sterilized"
}

# ===========================================================================
# Failure handling
# ===========================================================================
function Invoke-EmergencyDestroy {
    Write-Host ""
    Write-Warn "Run did not complete. Tearing down so nothing is left orphaned."

    if (-not (Test-Path $LocalState)) {
        Write-Warn "No local state file - nothing for tofu to destroy."
        Write-Warn "Check Proxmox by hand for VMs 100-102."
        return
    }

    Push-Location $ClusterDir
    try {
        & tofu destroy -input=false -auto-approve
        if ($LASTEXITCODE -ne 0) {
            Write-Warn "tofu destroy failed. Check Proxmox manually for VMs 100-102 before re-running."
        }
        else {
            Write-Ok "infrastructure destroyed cleanly"
        }
    }
    finally {
        Pop-Location
    }
}

# ===========================================================================
# Driver
# ===========================================================================
if ($Phase) { $toRun = @($Phase) }
elseif ($From) { $toRun = @($AllPhases[$AllPhases.IndexOf($From)..($AllPhases.Count - 1)]) }
else { $toRun = $AllPhases }

if ($WhatIfPhase) {
    Write-Host "`nPhases that would run:`n" -ForegroundColor Cyan
    $i = 0
    foreach ($p in $toRun) { $i++; Write-Host ("  {0}. {1}" -f $i, $p) }
    Write-Host ""
    return
}

$completed = $false
try {
    foreach ($p in $toRun) {
        switch ($p) {
            'Render' { Invoke-PhaseRender }
            'Overlay' { Invoke-PhaseOverlay }
            'Hypervisor' { Invoke-PhaseHypervisor }
            'Verify' { Invoke-PhaseVerify }
            'Compute' { Invoke-PhaseCompute }
            'Cluster' { Invoke-PhaseCluster }
            'Migrate' { Invoke-PhaseMigrate }
            'Backup' { Invoke-PhaseBackup }
            'Sterilize' { Invoke-PhaseSterilize }
        }
    }
    $completed = $true

    Write-Host ""
    Write-Host "Ignition complete. The cluster is now self-sustaining." -ForegroundColor Green
    Write-Host ""
}
catch {
    Write-Host ""
    Write-Host "HALTED: $_" -ForegroundColor Red

    if ($KeepOnFailure) {
        Write-Warn "-KeepOnFailure set: leaving state and secrets in place for debugging."
        Write-Warn "Run '.\scripts\Start-Homelab.ps1 -Phase Sterilize' when you are done."
        exit 1
    }

    # Only auto-destroy if this run could actually have created infrastructure.
    if ($toRun -contains 'Compute') { Invoke-EmergencyDestroy }
    Invoke-PhaseSterilize -Quiet
    exit 1
}
finally {
    # Belt and braces: on a successful full run the Sterilize phase has already
    # cleaned up, but a partial run may leave secrets on disk. Never do that.
    if ($completed -and $toRun -notcontains 'Sterilize' -and -not $KeepOnFailure) {
        Invoke-PhaseSterilize -Quiet
    }
}

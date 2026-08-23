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

    # Which entry in the config's sites[] array to deploy. The index IS the
    # site's identity: it names the site, picks its network, numbers its VMs.
    [int]$SiteIndex = 0,

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

# Addressing is derived from the site index, never hardcoded - two sites advertising
# the same subnet onto one tailnet collide, and the symptom looks like a broken
# network rather than a config mistake.
# Addressing is derived from the site index, never configured. Two sites cannot
# share an octet because two array entries cannot share an index, so the
# collision that would present as a broken overlay network is not expressible.
#
# OpenTofu asserts the same invariants at plan time (registry.tf); this is the
# fast-feedback copy, so a mistake fails in a second rather than after a
# provider round trip.
function Get-SiteNetwork {
    param([Parameter(Mandatory)][int]$Index)

    $cfg = Read-RenderedConfig
    $sites = @($cfg.sites)

    if ($sites.Count -eq 0) { throw "The config defines no sites. Add one to sites[] in config/management.tpl.json." }
    if ($Index -lt 0 -or $Index -ge $sites.Count) {
        throw "site index $Index is out of range: the config defines $($sites.Count) site(s), so valid indices are 0-$($sites.Count - 1)."
    }
    if ($Index -gt 85) {
        throw "site index $Index is too high. The octet is 10 + index and must stay below 96, where the Kubernetes defaults begin."
    }

    $site = $sites[$Index]
    $nodes = @($site.hypervisor.nodes)
    if ($nodes.Count -eq 0) { throw "Site $Index has no hypervisor nodes. Add at least one to sites[$Index].hypervisor.nodes." }
    if ($site.control_plane_count -lt 1) { throw "Site $Index has control_plane_count $($site.control_plane_count); it must be at least 1." }

    $name = "site$Index"
    $o = 10 + $Index
    # A human label from the vault, so it never reaches git. Falls back to the
    # positional name when the field is absent.
    $label = if ([string]::IsNullOrWhiteSpace($site.name)) { $name } else { $site.name }

    [pscustomobject]@{
        Name        = $name
        Label       = $label
        SiteCidr    = "10.$o.0.0/16"      # advertised as one route
        NodeCidr    = "10.$o.10.0/24"     # Talos control plane
        Gateway     = "10.$o.10.1"
        DhcpStart   = "10.$o.10.50"
        DhcpEnd     = "10.$o.10.99"
        NodeIps     = @(0..($site.control_plane_count - 1) | ForEach-Object { "10.$o.10.$(100 + $_)" })
        VmNames     = @(0..($site.control_plane_count - 1) | ForEach-Object { "$name-cp-{0:d2}" -f ($_ + 1) })
        Hypervisors = $nodes
    }
}

# OpenTofu reads the same config; this tells it which site to use.
$env:TF_VAR_site_index = $SiteIndex

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

function Read-RenderedConfig {
    if (-not (Test-Path $ConfigRendered)) {
        throw "Rendered config not found. Run the Render phase first."
    }
    Get-Content $ConfigRendered -Raw | ConvertFrom-Json
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

    # Fail early and clearly if the CLI is not signed in, rather than letting
    # 'op inject' emit a half-rendered file.
    & op whoami 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "1Password CLI is not signed in. Run:  op signin"
    }

    Write-Info "rendering config\management.rendered.json"
    Invoke-Native { op inject -i $ConfigTpl -o $ConfigRendered -f } '1Password inject (config)'

    # The inventory is generated, not templated: op inject substitutes into a
    # fixed file and cannot loop over sites[].hypervisor.nodes. Generating it is
    # what makes appending a node genuinely sufficient.
    $Net = Get-SiteNetwork -Index $SiteIndex

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
        Invoke-Native { tofu init -input=false } 'tofu init'

        # Applied ahead of the playbook so the hypervisor can log in with a
        # tagged key. The tag is what makes autoApprovers approve the subnet
        # route without anyone touching the admin console.
        Write-Info "minting a tagged auth key"
        Invoke-Native {
            tofu apply -input=false -auto-approve -target=tailscale_tailnet_key.hypervisor
        } 'tofu apply (overlay network)'

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
    $cmd = "export PATH=`$HOME/.local/bin:`$PATH; cd '$wslRepo' && " +
           "ansible-playbook -i inventory.yml hypervisor-prep.yml $siteVars $keyVars $extraVars"

    # With no -d, WSL uses the default distro, whatever it happens to be.
    $wslArgs = if ($WslDistro) { @('-d', $WslDistro, '--', 'bash', '-lc', $cmd) }
               else { @('--', 'bash', '-lc', $cmd) }

    Write-Info ("running the playbook inside WSL" + $(if ($WslDistro) { " ($WslDistro)" } else { " (default distro)" }))
    Write-Info "you will be prompted for the Proxmox host's sudo password"
    Invoke-Native { wsl @wslArgs } 'ansible-playbook'

    Write-Ok "hypervisor configured"
}

# ===========================================================================
# PHASE 4 - Verify
# ===========================================================================
function Invoke-PhaseVerify {
    Write-Phase 'Verify' 'Prove the network works before spending time on OpenTofu.'

    $Net = Get-SiteNetwork -Index $SiteIndex
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
        Invoke-Native { tofu init -input=false } 'tofu init'

        # Build the VMs only. Splitting this from the Talos phase means a
        # failure here is obviously a Proxmox problem, not a Talos one.
        Write-Info "creating the ISO and the virtual machines"
        Invoke-Native {
            tofu apply -input=false -auto-approve `
                -target=proxmox_virtual_environment_file.talos_iso `
                -target=proxmox_virtual_environment_vm.talos_cp
        } 'tofu apply (compute)'

        Write-Ok "VMs created"

        # The provider returns as soon as Proxmox defines the VM. Talos still
        # has to boot. Poll its API port rather than guessing at a sleep.
        foreach ($node in (Get-SiteNetwork -Index $SiteIndex).NodeIps) {
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
        Invoke-Native {
            tofu apply -input=false -auto-approve `
                -target=talos_machine_configuration_apply.control_plane
        } 'tofu apply (talos config)'

        Write-Info "bootstrapping etcd"
        Invoke-Native {
            tofu apply -input=false -auto-approve -target=talos_machine_bootstrap.this
        } 'tofu apply (bootstrap)'

        # Everything else, including the Flux bootstrap. Flux goes last because
        # its provider is configured from the kubeconfig the previous steps
        # produce.
        Write-Info "installing Flux and finishing the apply"
        Invoke-Native { tofu apply -input=false -auto-approve } 'tofu apply (flux)'

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
        Invoke-Native {
            tofu init -input=false -migrate-state -force-copy -backend-config="conn_str=$connStr"
        } 'tofu init -migrate-state'

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
    $site = $cfg.sites[$SiteIndex]
    $store = $site.object_storage

    foreach ($field in 'account_id', 'access_key_id', 'secret_access_key', 'bucket') {
        if ([string]::IsNullOrWhiteSpace($store.$field)) {
            throw "sites[$SiteIndex].object_storage.$field is missing from the rendered config."
        }
    }
    if ([string]::IsNullOrWhiteSpace($site.state.backup_recipient)) {
        throw @"
No 'state.backup_recipient' for site $SiteIndex in the rendered config.

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
        Write-Info "restore with: rclone cat R2:$($store.bucket)/management-cluster/latest.tfstate.age | age -d -i state-backup.key"
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

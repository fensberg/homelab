import io
p = 'scripts/Start-Homelab.ps1'
s = io.open(p, encoding='utf-8').read()

old = '''    $cfg = Read-RenderedConfig

    if (-not $cfg.state -or [string]::IsNullOrWhiteSpace($cfg.state.conn_str)) {
        throw @"
No 'state.conn_str' in the rendered config.

The Postgres backend needs a connection string, and a Postgres must be running
on the cluster for Flux to have reconciled. Neither exists yet - see the
'Deferred' section of docs\epochs\01-ignition.md.

Once it does, add the item to 1Password at op://homelab/state-postgres/conn_str.
"@
    }

    Push-Location $ClusterDir
    try {
        # Parse host:port out of postgres://user:pass@host:port/db
        if ($cfg.state.conn_str -match '@([^:/@]+):(\d+)') {
            $pgHost = $Matches[1]; $pgPort = [int]$Matches[2]
        }
        else {
            throw "Could not parse a host and port out of state.conn_str."
        }
'''
new = '''    Push-Location $ClusterDir
    try {
        # Derived from variables.tf and the 1Password password, not stored as a
        # secret of its own - see the state_conn_str output in database.tf.
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
'''
assert old in s, 'migrate preamble not found'
s = s.replace(old, new)

old2 = '''        Write-Info "migrating state (local -> Postgres)"
        $connStr = $cfg.state.conn_str
        Invoke-Native {'''
new2 = '''        Write-Info "migrating state (local -> Postgres)"
        Invoke-Native {'''
assert old2 in s, 'migrate apply not found'
s = s.replace(old2, new2)

io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('Start-Homelab.ps1 patched')

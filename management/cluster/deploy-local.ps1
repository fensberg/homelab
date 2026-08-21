$ErrorActionPreference = "Stop"
$renderedConfig = "..\..\config\management.rendered.json"

try {
    Write-Host "[INFO] Loading global environment variables..." -ForegroundColor Cyan
    Get-Content ..\..\.env | ForEach-Object {
        if ($_ -match "^([^=]+)=(.*)$") {
            $name = $matches[1]
            $value = $matches[2].Replace('"', '')
            [System.Environment]::SetEnvironmentVariable($name, $value)
        }
    }

    Write-Host "[INFO] Authenticating to 1Password and generating template..." -ForegroundColor Cyan
    op inject -i ..\..\config\management.tpl.json -o $renderedConfig
    if ($LASTEXITCODE -ne 0) { throw "1Password injection failed" }

    Write-Host "[INFO] Initializing OpenTofu..." -ForegroundColor Cyan
    tofu init
    if ($LASTEXITCODE -ne 0) { throw "OpenTofu init failed" }

    Write-Host "[INFO] Bootstrapping Layer 1 (Compute) and Layer 2 (Flux)..." -ForegroundColor Cyan
    tofu apply -auto-approve
    if ($LASTEXITCODE -ne 0) { throw "OpenTofu apply failed" }

    Write-Host "[SUCCESS] Management Cluster deployed successfully." -ForegroundColor Green

} catch {
    Write-Host "`n[ALERT] Execution halted: $_" -ForegroundColor Red
    exit 1
} finally {
    # GUARANTEED EXECUTION: Runs on success, error, or Ctrl+C interruption
    Write-Host "[INFO] Initializing mandatory workspace sterilization..." -ForegroundColor Yellow
    if (Test-Path $renderedConfig) {
        Remove-Item -Force $renderedConfig
        Write-Host "[SUCCESS] Rendered secrets successfully purged from disk." -ForegroundColor Green
    }
}

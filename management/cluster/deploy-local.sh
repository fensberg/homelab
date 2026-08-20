#!/bin/bash
set -e

cleanup() {
	echo "🚨 Execution finished or halted. Triggering secure cleanup..."
	rm -f ../../config/management.rendered.json
	rm -f terraform.tfstate terraform.tfstate.backup
	echo "✅ Workspace sterilized."
}
trap cleanup EXIT INT TERM HUP
# ==============================================================================

echo "🌐 Loading global environment variables..."
source ../../.env

echo "🔐 Authenticating to 1Password and generating template..."
op inject -i ../../config/management.tpl.json -o ../../config/management.rendered.json

echo "🏗️ Initializing OpenTofu..."
tofu init

echo "🚀 Bootstrapping Layer 1 (Compute) & Layer 2 (Flux)..."
tofu apply -auto-approve

echo "🎯 Management Cluster deployed successfully."

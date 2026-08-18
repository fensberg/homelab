#!/bin/bash
set -e

cleanup() {
    echo "🚨 Execution halted or finished. Triggering cleanup..."

    # Wipe the local state files from the disk
    rm -f terraform.tfstate terraform.tfstate.backup
    rm -f /tmp/kubeconfig

    # Disable the PG backend file again so the repo returns to a clean state
    if [ -f "backend_pg.tf" ]; then
        mv backend_pg.tf backend_pg.tf.disabled
    fi

    # Unset memory variables
    unset PG_CONN_STR
    unset KUBECONFIG
}
trap cleanup EXIT INT TERM HUP
# ==============================================================================

echo "🚀 Initializing Management Cluster with Secure Local State..."
# Because backend_pg is disabled, OpenTofu defaults to local state
tofu init
tofu apply -auto-approve

echo "🔐 Extracting temporary Kubeconfig to RAM..."
tofu output -raw kubeconfig > /tmp/kubeconfig
chmod 600 /tmp/kubeconfig
export KUBECONFIG="/tmp/kubeconfig"

echo "⏳ Waiting for CloudNativePG database pod to become ready..."
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=cloudnative-pg -n database-system --timeout=300s

echo "🌐 Fetching dynamic Postgres connection string from the cluster..."
PG_PASSWORD=$(kubectl get secret management-state-db-app -n database-system -o jsonpath="{.data.password}" | base64 --decode)
export PG_CONN_STR="postgres://app:${PG_PASSWORD}@management-state-db-rw.database-system.svc.cluster.local:5432/app"

echo "🚚 Activating Postgres Backend and Migrating State..."
# Enable the Postgres backend block
mv backend_pg.tf.disabled backend_pg.tf

# Instruct OpenTofu to push the local state file into the database
tofu init -migrate-state -force-copy

echo "✅ Management cluster state is now securely self-hosted in Postgres."
# The trap will automatically fire here and delete the local terraform.tfstate

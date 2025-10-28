#!/bin/bash
# CLD-REQ-063: Generate secure keys for secrets management
# This script generates cryptographically secure keys for production use

set -euo pipefail

echo "========================================="
echo "Cloudless Secrets Management Key Generator"
echo "========================================="
echo ""
echo "WARNING: These keys protect sensitive data. Store them securely!"
echo "  - Use a secrets manager (HashiCorp Vault, AWS Secrets Manager, etc.)"
echo "  - Never commit these keys to version control"
echo "  - Rotate keys regularly (recommended: every 90 days)"
echo ""

# Function to generate random base64 key
generate_key() {
    local bytes=$1
    openssl rand -base64 $bytes | tr -d '\n'
}

# Generate master encryption key (32 bytes)
echo "Generating Master Encryption Key..."
MASTER_KEY=$(generate_key 32)
MASTER_KEY_ID="key-$(uuidgen | tr '[:upper:]' '[:lower:]')"

# Generate JWT signing key (32 bytes)
echo "Generating JWT Token Signing Key..."
SIGNING_KEY=$(generate_key 32)

echo ""
echo "========================================="
echo "Generated Keys (Base64 Encoded)"
echo "========================================="
echo ""

echo "# Master Encryption Key (AES-256-GCM)"
echo "CLOUDLESS_SECRETS_MASTER_KEY=${MASTER_KEY}"
echo "CLOUDLESS_SECRETS_MASTER_KEY_ID=${MASTER_KEY_ID}"
echo ""

echo "# JWT Token Signing Key"
echo "CLOUDLESS_SECRETS_TOKEN_SIGNING_KEY=${SIGNING_KEY}"
echo ""

echo "========================================="
echo "Docker Compose Usage"
echo "========================================="
echo ""
echo "Add these to a .env file in deployments/docker/:"
echo ""
cat <<EOF
# Secrets Management Keys (CLD-REQ-063)
# Generated: $(date -u +"%Y-%m-%d %H:%M:%S UTC")
CLOUDLESS_SECRETS_MASTER_KEY=${MASTER_KEY}
CLOUDLESS_SECRETS_MASTER_KEY_ID=${MASTER_KEY_ID}
CLOUDLESS_SECRETS_TOKEN_SIGNING_KEY=${SIGNING_KEY}
EOF

echo ""
echo "========================================="
echo "Kubernetes Secret Usage"
echo "========================================="
echo ""
echo "Create a Kubernetes secret:"
echo ""
cat <<EOF
kubectl create secret generic cloudless-secrets-keys \\
  --from-literal=master-key="${MASTER_KEY}" \\
  --from-literal=master-key-id="${MASTER_KEY_ID}" \\
  --from-literal=signing-key="${SIGNING_KEY}" \\
  --namespace=cloudless-system
EOF

echo ""
echo ""
echo "========================================="
echo "Security Best Practices"
echo "========================================="
echo ""
echo "1. Store keys in a secure secrets manager"
echo "2. Enable key rotation (CLOUDLESS_SECRETS_ROTATION_ENABLED=true)"
echo "3. Set rotation interval (CLOUDLESS_SECRETS_ROTATION_INTERVAL=2160h)"
echo "4. Monitor audit logs for secret access"
echo "5. Use short-lived access tokens (CLOUDLESS_SECRETS_TOKEN_TTL=1h)"
echo "6. Restrict secret audiences to specific workloads"
echo "7. Use mTLS for all coordinator-agent communication"
echo ""

#!/bin/bash
set -euo pipefail

# Bootstrap script for step-ca PKI setup
# This script generates the CA certificates and uploads them to GCP Secret Manager

PROJECT_ID="${PROJECT_ID:-$(gcloud config get-value project)}"
REGION="${REGION:-us-east4}"
CA_NAME="${CA_NAME:-MyGCPCA}"
DNS_NAME="${DNS_NAME:-ca.example.com}"

echo "==> Bootstrapping step-ca PKI for project: $PROJECT_ID"

# Create a temporary directory for PKI files
TEMP_DIR=$(mktemp -d)
trap "rm -rf $TEMP_DIR" EXIT

export STEPPATH="$TEMP_DIR"

# Generate random passwords
openssl rand -base64 32 > "$TEMP_DIR/password"
openssl rand -base64 32 > "$TEMP_DIR/provisioner-password"

echo "==> Initializing PKI with step ca init"
step ca init \
  --name="$CA_NAME" \
  --dns="$DNS_NAME" \
  --address=":9000" \
  --provisioner="admin" \
  --password-file="$TEMP_DIR/password" \
  --provisioner-password-file="$TEMP_DIR/provisioner-password" \
  --ssh

echo "==> Uploading secrets to GCP Secret Manager"

# Upload root CA certificate
gcloud secrets versions add step-root-ca \
  --project="$PROJECT_ID" \
  --data-file="$TEMP_DIR/certs/root_ca.crt" || \
gcloud secrets create step-root-ca \
  --project="$PROJECT_ID" \
  --data-file="$TEMP_DIR/certs/root_ca.crt"

# Upload intermediate CA certificate
gcloud secrets versions add step-intermediate-crt \
  --project="$PROJECT_ID" \
  --data-file="$TEMP_DIR/certs/intermediate_ca.crt" || \
gcloud secrets create step-intermediate-crt \
  --project="$PROJECT_ID" \
  --data-file="$TEMP_DIR/certs/intermediate_ca.crt"

# Upload intermediate CA key
gcloud secrets versions add step-intermediate-key \
  --project="$PROJECT_ID" \
  --data-file="$TEMP_DIR/secrets/intermediate_ca_key" || \
gcloud secrets create step-intermediate-key \
  --project="$PROJECT_ID" \
  --data-file="$TEMP_DIR/secrets/intermediate_ca_key"

# Upload SSH host CA key
gcloud secrets versions add step-ssh-host-ca-key \
  --project="$PROJECT_ID" \
  --data-file="$TEMP_DIR/secrets/ssh_host_ca_key" || \
gcloud secrets create step-ssh-host-ca-key \
  --project="$PROJECT_ID" \
  --data-file="$TEMP_DIR/secrets/ssh_host_ca_key"

# Upload SSH user CA key
gcloud secrets versions add step-ssh-user-ca-key \
  --project="$PROJECT_ID" \
  --data-file="$TEMP_DIR/secrets/ssh_user_ca_key" || \
gcloud secrets create step-ssh-user-ca-key \
  --project="$PROJECT_ID" \
  --data-file="$TEMP_DIR/secrets/ssh_user_ca_key"

# Upload ca.json (will be updated after terraform apply)
gcloud secrets versions add step-ca-config \
  --project="$PROJECT_ID" \
  --data-file="$TEMP_DIR/config/ca.json" || \
gcloud secrets create step-ca-config \
  --project="$PROJECT_ID" \
  --data-file="$TEMP_DIR/config/ca.json"

# Upload intermediate CA password
gcloud secrets versions add step-intermediate-password \
  --project="$PROJECT_ID" \
  --data-file="$TEMP_DIR/password" || \
gcloud secrets create step-intermediate-password \
  --project="$PROJECT_ID" \
  --data-file="$TEMP_DIR/password"

echo ""
echo "==> Bootstrap complete!"
echo ""
echo "Root CA fingerprint (save this for clients):"
step certificate fingerprint "$TEMP_DIR/certs/root_ca.crt"
echo ""
echo "Next steps:"
echo "  1. Run 'terraform apply' to deploy the infrastructure"
echo "  2. After deployment, update the ca.json secret with the actual Cloud Run URL and DB password"
echo "  3. Use 'step ca bootstrap --ca-url https://YOUR-SERVICE.run.app --fingerprint FINGERPRINT' on clients"

#!/bin/bash
set -euo pipefail

cd /Users/jason/git/terraform-playground/step-ca
mkdir -p secrets

TEMP_DIR=$(mktemp -d)
export STEPPATH="$TEMP_DIR"

# Generate root and intermediate CAs manually with no password
step certificate create "Root CA" \
  "$TEMP_DIR/root_ca.crt" \
  "$TEMP_DIR/root_ca_key" \
  --profile root-ca \
  --no-password --insecure

step certificate create "Intermediate CA" \
  "$TEMP_DIR/intermediate_ca.crt" \
  "$TEMP_DIR/intermediate_ca_key" \
  --profile intermediate-ca \
  --ca "$TEMP_DIR/root_ca.crt" \
  --ca-key "$TEMP_DIR/root_ca_key" \
  --no-password --insecure

# Generate SSH CA keys
step crypto keypair "$TEMP_DIR/ssh_user_ca_key.pub" "$TEMP_DIR/ssh_user_ca_key" \
  --kty EC --curve P-256 --no-password --insecure

step crypto keypair "$TEMP_DIR/ssh_host_ca_key.pub" "$TEMP_DIR/ssh_host_ca_key" \
  --kty EC --curve P-256 --no-password --insecure

# Generate a JWK provisioner
step crypto jwk create "$TEMP_DIR/provisioner.pub" "$TEMP_DIR/provisioner.key" \
  --kty EC --curve P-256 --no-password --insecure

# Read the JWK
PROVISIONER_JWK=$(cat "$TEMP_DIR/provisioner.key")

# Copy all files to secrets directory
cp "$TEMP_DIR/root_ca.crt" secrets/step-root-ca
cp "$TEMP_DIR/intermediate_ca.crt" secrets/step-intermediate-crt
cp "$TEMP_DIR/intermediate_ca_key" secrets/step-intermediate-key
cp "$TEMP_DIR/ssh_host_ca_key" secrets/step-ssh-host-ca-key
cp "$TEMP_DIR/ssh_user_ca_key" secrets/step-ssh-user-ca-key
echo -n "" > secrets/step-intermediate-password

# Create ca.json
cat > secrets/step-ca-config <<EOFCONFIG
{
  "root": "/home/step/secrets/root_ca.crt",
  "crt": "/home/step/secrets/intermediate_ca.crt",
  "key": "/home/step/secrets/intermediate_ca_key",
  "password": "",
  "address": ":9000",
  "dnsNames": ["ca.example.com"],
  "ssh": {
    "hostKey": "/home/step/secrets/ssh_host_ca_key",
    "userKey": "/home/step/secrets/ssh_user_ca_key"
  },
  "logger": {
    "format": "json"
  },
  "db": {
    "type": "badgerv2",
    "dataSource": "/home/step/db"
  },
  "authority": {
    "provisioners": [
      {
        "type": "JWK",
        "name": "admin",
        "key": $PROVISIONER_JWK,
        "claims": {
          "enableSSHCA": true
        }
      }
    ]
  },
  "tls": {
    "cipherSuites": [
      "TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256",
      "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256"
    ],
    "minVersion": 1.2,
    "maxVersion": 1.3,
    "renegotiation": false
  }
}
EOFCONFIG

echo "Root CA fingerprint (save this):"
step certificate fingerprint "$TEMP_DIR/root_ca.crt"
echo ""
echo "Files saved to secrets/"
ls -lh secrets/

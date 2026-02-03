# step-ca on Cloud Run with Cloud SQL

Deploy step-ca PKI to Google Cloud Run, backed by Cloud SQL for state management.

## Prerequisites

- `gcloud` CLI authenticated
- `terraform` installed
- `step` CLI installed (`brew install step`)
- GCP project with billing enabled

## Deployment Steps

### 1. Bootstrap the PKI

Generate CA certificates and SSH keys, then upload to Secret Manager:

```bash
cd step-ca/
./bootstrap.sh
```

Save the root CA fingerprint output - you'll need it for client setup.

### 2. Deploy Infrastructure

```bash
terraform init
terraform apply
```

This creates:
- Cloud SQL PostgreSQL instance
- VPC network with private service connection
- Secret Manager secrets (populated by bootstrap script)
- Cloud Run service with step-ca
- Artifact Registry repository for the container image

### 3. Update Configuration

After deployment, update the `step-ca-config` secret with the actual Cloud Run URL and database password:

```bash
# Get outputs
CLOUD_RUN_URL=$(terraform output -raw step_ca_url)
DB_PASSWORD=$(terraform output -raw db_password)

# Create updated ca.json
cat > ca.json <<EOF
{
  "address": ":9000",
  "dnsNames": ["${CLOUD_RUN_URL#https://}"],
  "db": {
    "type": "postgresql",
    "dataSource": "host=/cloudsql/$(terraform output -raw cloud_sql_connection_name) user=step password=${DB_PASSWORD} dbname=step_ca sslmode=disable"
  },
  "crt": "/home/step/secrets/intermediate_ca.crt",
  "key": "/home/step/secrets/intermediate_ca.key",
  "root": "/home/step/secrets/root_ca.crt",
  "password": "",
  "authority": <copy from existing secret>,
  "tls": <copy from existing secret>,
  "ssh": {
    "hostKey": "/home/step/secrets/ssh_host_ca_key",
    "userKey": "/home/step/secrets/ssh_user_ca_key"
  }
}
EOF

gcloud secrets versions add step-ca-config --data-file=ca.json
```

Force a new Cloud Run revision to pick up the updated config:

```bash
gcloud run services update step-ca --region us-east4
```

## Client Setup

On client machines that need to use the CA:

```bash
# Bootstrap with your CA
step ca bootstrap --ca-url https://YOUR-SERVICE.run.app --fingerprint <FINGERPRINT>

# Get an SSH certificate
step ssh login user@example.com
```

## Architecture

- **Compute**: Cloud Run (serverless, auto-scaling)
- **Database**: Cloud SQL PostgreSQL (for certificate state/revocation)
- **Secrets**: Secret Manager (for CA keys and configuration)
- **Networking**: VPC with Cloud SQL private IP, VPC connector for Cloud Run

## Notes

- Cloud Run handles TLS termination, step-ca runs in insecure mode internally
- All CA keys are stored in Secret Manager, mounted as files at runtime
- Database password is randomly generated and stored in Terraform state
- The service is publicly accessible but requires valid provisioner credentials

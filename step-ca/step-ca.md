Deploying a PKI (Public Key Infrastructure) like `step-ca` on serverless platforms (Cloud Run) or managed K8s (GKE) is notoriously difficult because PKI is inherently **stateful** (it needs to keep track of keys, tokens, and certs), while those platforms prefer **stateless** workloads.

The easiest, most stable path to "just get it working" for SSH certificates is **Cloud Run backed by Cloud SQL**, with your keys stored in **Secret Manager**. This avoids the complexity of managing GKE persistent volumes and is cheaper/easier to maintain.

Here is a streamlined guide to deploying `step-ca` securely on GCP.

### The Architecture

  * **Compute:** Cloud Run (Serverless, handles HTTPS automatically).
  * **State/Database:** Cloud SQL (PostgreSQL). `step-ca` needs a DB to store used tokens and certificate revocation lists.
  * **Secrets:** GCP Secret Manager. Stores your Root CA key, Intermediate CA Key, and provisioner passwords.

-----

### Phase 1: Local Bootstrap

Do not try to run `step ca init` inside Cloud Run. Run it locally to generate the keys and config, then upload them.

1.  **Install `step` CLI locally** and initialize your PKI:

    ```bash
    # Initialize PKI (choose 'stand-alone' -> 'PostgreSQL' when asked for DB)
    step ca init --name="MyGCPCA" --dns="ca.example.com" --address=":9000" --provisioner="admin"
    ```

    *Note: When asked for the DB address, put a placeholder like `postgresql://step:password@localhost:5432/step_db`. We will override this later.*

2.  **Enable SSH Certificates:**

    ```bash
    step ca provisioner add ssh-user --type=ssh --user
    step ca provisioner add ssh-host --type=ssh --host
    ```

3.  **Locate your files:** You should now have a `$(step path)/secrets` and `$(step path)/config` directory containing:

      * `root_ca.crt`
      * `intermediate_ca.crt`
      * `intermediate_ca.key`
      * `password` (files containing your key passwords)
      * `ca.json` (the config file)

### Phase 2: Upload Secrets to GCP

Go to **GCP Secret Manager** and create secrets for the sensitive files you just generated. This allows Cloud Run to mount them as files.

Create the following secrets (names are suggestions):

  * `step-ca-config`: Paste the content of your `ca.json`.
  * `step-root-ca`: Paste content of `root_ca.crt`.
  * `step-intermediate-crt`: Paste content of `intermediate_ca.crt`.
  * `step-intermediate-key`: Paste content of `intermediate_ca.key`.
  * `step-password`: Paste the password used for your intermediate key.

### Phase 3: Prepare the Database

1.  Create a **Cloud SQL (PostgreSQL)** instance.
2.  Create a database named `step_ca`.
3.  Create a user `step` with a password.
4.  **Crucial:** Note the "Connection Name" of the instance (e.g., `project-id:us-central1:instance-name`).

### Phase 4: The Dockerfile

You need a custom Docker wrapper to point `step-ca` to the mounted secrets.

Create a `Dockerfile`:

```dockerfile
FROM smallstep/step-ca:latest

USER root
# Create directories for the secret mounts
RUN mkdir -p /home/step/secrets /home/step/config
RUN chown -R step:step /home/step

USER step

# We will mount the secrets to these locations via Cloud Run config
ENV STEPPATH=/home/step

# The entrypoint command
CMD ["/usr/local/bin/step-ca", "/home/step/config/ca.json"]
```

Build and push this image to Google Artifact Registry:

```bash
gcloud builds submit --tag gcr.io/YOUR_PROJECT/step-ca-cloudrun
```

### Phase 5: Deploy to Cloud Run

This is where the magic happens. You will deploy the image and "mount" the secrets as files.

Run this command (replace placeholders):

```bash
gcloud run deploy step-ca \
  --image gcr.io/YOUR_PROJECT/step-ca-cloudrun \
  --region us-central1 \
  --allow-unauthenticated \
  --port 9000 \
  --add-cloudsql-instances "PROJECT:REGION:INSTANCE" \
  --set-env-vars "STEP_TLS_INSECURE=true" \
  --set-secrets "/home/step/config/ca.json=step-ca-config:latest" \
  --set-secrets "/home/step/secrets/root_ca.crt=step-root-ca:latest" \
  --set-secrets "/home/step/secrets/intermediate_ca.crt=step-intermediate-crt:latest" \
  --set-secrets "/home/step/secrets/intermediate_ca.key=step-intermediate-key:latest" \
  --set-secrets "/home/step/secrets/password=step-password:latest"
```

**Key Configuration Details:**

  * **`STEP_TLS_INSECURE=true`**: Cloud Run handles the TLS/SSL termination at the load balancer layer (the `https://...run.app` URL). The container itself receives HTTP. `step-ca` needs to know this is okay.
  * **`--add-cloudsql-instances`**: This exposes the Cloud SQL socket to the container.

### Phase 6: Final Configuration Fix

Your `ca.json` (inside the secret) still points to localhost for the DB and has local paths for keys. You need to edit the `step-ca-config` secret in Secret Manager to match the Cloud Run environment.

Update the `ca.json` in Secret Manager to look like this:

```json
{
  "address": ":9000",
  "dnsNames": ["your-service-url.a.run.app"],
  "db": {
    "type": "postgresql",
    "dataSource": "host=/cloudsql/PROJECT:REGION:INSTANCE user=step password=DB_PASSWORD dbname=step_ca sslmode=disable"
  },
  "crt": "/home/step/secrets/intermediate_ca.crt",
  "key": "/home/step/secrets/intermediate_ca.key",
  "root": "/home/step/secrets/root_ca.crt",
  "password": "/home/step/secrets/password",
  "authority": { ... }
}
```

*Redeploy the Cloud Run revision after updating the secret so it picks up the changes.*

### How to generate your SSH Certs now

1.  **Bootstrap your local client** with the new remote CA:

    ```bash
    step ca bootstrap --ca-url https://your-service.a.run.app --fingerprint <ROOT_CA_FINGERPRINT>
    ```

2.  **Login/Generate:**

    ```bash
    # Get a short-lived SSH cert
    step ssh login user@example.com --provisioner ssh-user
    ```

### Summary of why this failed before

  * **Filesystem:** You likely tried to generate keys *inside* the container. On Cloud Run/GKE, those vanish on restart.
  * **TLS:** `step-ca` tries to do its own TLS by default. On Cloud Run, you must offload TLS to Google and run the container in "insecure" mode (checking the `STEP_TLS_INSECURE` env var).

**Would you like me to elaborate on how to configure the client-side `ssh` config to automatically use these certificates?**

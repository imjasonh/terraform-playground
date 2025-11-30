# container-vm

Terraform module that creates a GCE instance template running Docker containers on Container-Optimized OS, managed via cloud-init and systemd.

This is intended to simulate and replace the [deprecated container startup agent](https://docs.cloud.google.com/compute/docs/containers/migrate-containers) using `cloud-init`.

## Features

- Runs one or more containers with systemd service management
- Supports Container-Optimized OS (COS) with pre-installed Docker
- Artifact Registry authentication via docker-credential-gcr
- Cloud Logging integration with gcplogs driver
- Shielded VM with secure boot enabled
- Requires digest-pinned container images for security

## Usage

```hcl
module "container_vm" {
  source = "./container-vm"

  project_id             = "my-project"
  region                 = "us-east4"
  network                = "default"
  subnetwork             = "default"
  service_account_email  = "my-sa@my-project.iam.gserviceaccount.com"

  containers = {
    web = {
      image = "us-east4-docker.pkg.dev/my-project/my-repo/app@sha256:abc123..."
      ports = ["8080:8080"]
      env = [{
        name  = "PORT"
        value = "8080"
      }]
    }
  }
}

// Next, create VM instances from this template, route traffic to them via GCLB, etc.
```

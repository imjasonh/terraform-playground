terraform {
  required_providers {
    cosign = { source = "chainguard-dev/cosign" }
    oci    = { source = "chainguard-dev/oci" }
  }
}

variable "name" { default = "gitea" }
variable "project" {}
variable "region" { default = "us-central1" }

provider "google" {
  project = var.project
  region  = var.region
}

resource "google_artifact_registry_repository" "repo" {
  repository_id = var.name
  format        = "DOCKER"
}

resource "random_id" "bucket" { byte_length = 8 }

resource "google_storage_bucket" "bucket" {
  name     = "gitea-bucket-${random_id.bucket.hex}"
  location = var.region
}

data "oci_ref" "upstream" { ref = "gitea/gitea" }

resource "cosign_copy" "copy" {
  source      = "${data.oci_ref.upstream.ref}@${data.oci_ref.upstream.digest}"
  destination = "${var.region}-docker.pkg.dev/${var.project}/${google_artifact_registry_repository.repo.name}/gitea"
}

resource "google_service_account" "sa" { account_id = "${var.name}-sa" }

resource "google_storage_bucket_iam_binding" "bucket" {
  bucket = google_storage_bucket.bucket.name
  role   = "roles/storage.objectAdmin"
  members = [
    "serviceAccount:${google_service_account.sa.email}",
  ]
}

// TODO: fails with
//  - error: chmod on /data/gitea/home/.gitconfig.lock failed: Operation not permitted

resource "google_cloud_run_v2_service" "svc" {
  name     = var.name
  location = var.region

  launch_stage = "BETA"

  template {
    scaling { max_instance_count = 1 }

    execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

    service_account = google_service_account.sa.email
    timeout         = "${60 * 60}s" // 1 hour
    containers {
      resources { cpu_idle = true }
      image = cosign_copy.copy.copied_ref
      ports {
        container_port = 3000
      }
      volume_mounts {
        name       = "gcs"
        mount_path = "/data"
      }
    }

    volumes {
      name = "gcs"
      gcs {
        bucket    = google_storage_bucket.bucket.name
        read_only = false
      }
    }
  }
}

// Allow anyone to access the service.
resource "google_cloud_run_service_iam_binding" "noauth" {
  service = google_cloud_run_v2_service.svc.name
  role    = "roles/run.invoker"
  members = ["allUsers"]
}

output "url" { value = google_cloud_run_v2_service.svc.uri }

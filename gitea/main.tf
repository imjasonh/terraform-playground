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

provider "google-beta" {
  project = var.project
  region  = var.region
}

resource "google_artifact_registry_repository" "repo" {
  repository_id = var.name
  format        = "DOCKER"
}

# Generate app.ini configuration
locals {
  app_ini_content = templatefile("${path.module}/app.ini.tpl", {
    domain         = "${var.name}-${var.project}.${var.region}.run.app"
    root_url       = "https://${var.name}-${var.project}.${var.region}.run.app"
    secret_key     = random_password.secret_key.result
    internal_token = random_password.internal_token.result
  })
}

resource "random_id" "bucket" { byte_length = 8 }

resource "google_storage_bucket" "bucket" {
  name     = "gitea-bucket-${random_id.bucket.hex}"
  location = var.region
}

# Generate secure tokens for Gitea configuration
resource "random_password" "secret_key" {
  length  = 64
  special = true
}

resource "random_password" "internal_token" {
  length  = 64
  special = true
}

resource "cosign_copy" "copy" {
  source      = provider::oci::get("gitea/gitea:latest").full_ref
  destination = "${var.region}-docker.pkg.dev/${var.project}/${google_artifact_registry_repository.repo.name}/gitea"
}
resource "google_service_account" "sa" { account_id = "${var.name}-sa" }

resource "google_storage_bucket_iam_binding" "bucket" {
  bucket  = google_storage_bucket.bucket.name
  role    = "roles/storage.objectAdmin"
  members = ["serviceAccount:${google_service_account.sa.email}"]
}

# Upload app.ini to GCS
resource "google_storage_bucket_object" "app_ini" {
  name    = "gitea/conf/app.ini"
  bucket  = google_storage_bucket.bucket.name
  content = local.app_ini_content
}

resource "google_cloud_run_v2_service" "svc" {
  provider = google-beta
  name     = var.name
  location = var.region

  launch_stage = "BETA"

  # Force new revision when app.ini changes
  depends_on = [google_storage_bucket_object.app_ini]

  template {
    scaling { max_instance_count = 1 }

    execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

    # Force new revision when app.ini changes
    annotations = {
      "app-ini-hash" = md5(local.app_ini_content)
    }

    service_account = google_service_account.sa.email
    timeout         = "${60 * 60}s" // 1 hour
    containers {
      resources { cpu_idle = true }
      image = cosign_copy.copy.copied_ref
      ports {
        container_port = 3000
      }

      # Add startup probe to ensure Gitea is ready
      startup_probe {
        http_get {
          path = "/"
          port = 3000
        }
        initial_delay_seconds = 10
        period_seconds        = 5
        timeout_seconds       = 3
        failure_threshold     = 30
      }


      # Use custom config from mounted volume
      env {
        name  = "GITEA_CUSTOM"
        value = "/data"
      }
      env {
        name  = "USER_UID"
        value = "1000"
      }
      env {
        name  = "USER_GID"
        value = "1000"
      }


      volume_mounts {
        name       = "gcs"
        mount_path = "/data"
      }
    }

    volumes {
      name = "gcs"
      gcs {
        bucket        = google_storage_bucket.bucket.name
        read_only     = false
        mount_options = ["uid=1000", "gid=1000", "file-mode=0755", "dir-mode=0755"]
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
output "image" { value = cosign_copy.copy.copied_ref }

output "github_oauth_callback_url" {
  value       = "${google_cloud_run_v2_service.svc.uri}/user/oauth2/github/callback"
  description = "GitHub OAuth callback URL to use when configuring your GitHub OAuth app"
}

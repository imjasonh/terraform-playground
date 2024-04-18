// TODO: state in GCS bucket

terraform {
  required_providers {
    ko = { source = "ko-build/ko" }
  }
}

locals {
  project_id = "jason-chainguard"
  region     = "us-central1"
}

provider "google" {
  project = local.project_id
  region  = local.region
}

provider "ko" { repo = "gcr.io/${local.project_id}/litestream/app" }

resource "google_storage_bucket" "bucket" {
  name     = "${local.project_id}-litestream"
  location = local.region
}

resource "google_service_account" "sa" {
  account_id   = "litestream"
  display_name = "Litestream Service Account"
}

resource "google_storage_bucket_iam_binding" "binding" {
  bucket  = google_storage_bucket.bucket.name
  role    = "roles/storage.admin"
  members = ["serviceAccount:${google_service_account.sa.email}"]
}

resource "ko_build" "build" {
  importpath  = "./"
  working_dir = path.module
  base_image  = "cgr.dev/chainguard/litestream"
}

resource "google_cloud_run_v2_service" "service" {
  provider = google-beta // for empty_dir

  name         = "litestream"
  location     = local.region
  launch_stage = "BETA"
  ingress      = "INGRESS_TRAFFIC_ALL"


  template {
    scaling { max_instance_count = 1 }
    max_instance_request_concurrency = 1000

    containers {
      image = ko_build.build.image_ref
      volume_mounts {
        name       = "data"
        mount_path = "/data"
      }
      env {
        name  = "BUCKET"
        value = google_storage_bucket.bucket.name
      }
    }

    containers {
      image = "chainguard/litestream"
      args  = ["replicate", "/data/db.sqlite", "gcs://${google_storage_bucket.bucket.name}/litestream"]
      volume_mounts {
        name       = "data"
        mount_path = "/data"
      }
    }

    volumes {
      name = "data"
      empty_dir {
        medium     = "MEMORY"
        size_limit = "256Mi"
      }
    }
  }
}

// Allow all users to invoke the service
resource "google_cloud_run_v2_service_iam_member" "public" {
  name   = google_cloud_run_v2_service.service.name
  role   = "roles/run.invoker"
  member = "allUsers"
}

output "url" { value = google_cloud_run_v2_service.service.uri }
output "app-image" { value = ko_build.build.image_ref }

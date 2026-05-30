// TODO: state in GCS bucket

terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
    ko = { source = "ko-build/ko" }
    oci = { source = "chainguard-dev/oci" }
  }
}

locals {
  project_id  = "jason-chainguard"
  region      = "us-central1"
  replica_url = "gs://${google_storage_bucket.bucket.name}/litestream"
}

provider "google" {
  project = local.project_id
  region  = local.region
}

provider "google-beta" {
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
  base_image  = "cgr.dev/chainguard/glibc-dynamic"
  env = [
    "CGO_ENABLED=1",
    "CC=zig cc -target x86_64-linux-gnu",
    "CXX=zig c++ -target x86_64-linux-gnu",
    "GOFLAGS=-tags=vfs",
  ]
}

resource "google_cloud_run_v2_service" "reader" {
  provider = google-beta

  name         = "litestream-reader"
  location     = local.region
  launch_stage = "BETA"
  ingress      = "INGRESS_TRAFFIC_ALL"

  template {
    scaling {
      max_instance_count = 10
    }

    max_instance_request_concurrency = 1000

    service_account = google_service_account.sa.email

    containers {
      image = ko_build.build.image_ref
      env {
        name  = "LITESTREAM_REPLICA_URL"
        value = local.replica_url
      }
      env {
        name  = "LITESTREAM_WRITE_ENABLED"
        value = "false"
      }
    }
  }
}

resource "google_cloud_run_v2_service" "writer" {
  provider = google-beta // for empty_dir

  name         = "litestream-writer"
  location     = local.region
  launch_stage = "BETA"
  ingress      = "INGRESS_TRAFFIC_ALL"

  template {
    scaling {
      max_instance_count = 1
    }

    max_instance_request_concurrency = 250

    service_account = google_service_account.sa.email

    containers {
      image = ko_build.build.image_ref
      volume_mounts {
        name       = "data"
        mount_path = "/data"
      }
      env {
        name  = "LITESTREAM_REPLICA_URL"
        value = local.replica_url
      }
      env {
        name  = "LITESTREAM_WRITE_ENABLED"
        value = "true"
      }
      env {
        name  = "LITESTREAM_BUFFER_PATH"
        value = "/data/db.sqlite"
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

resource "google_cloud_run_v2_service_iam_member" "reader_public" {
  name     = google_cloud_run_v2_service.reader.name
  location = google_cloud_run_v2_service.reader.location
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_cloud_run_v2_service_iam_member" "writer_public" {
  name     = google_cloud_run_v2_service.writer.name
  location = google_cloud_run_v2_service.writer.location
  role     = "roles/run.invoker"
  member   = "allUsers"
}

output "reader_url" { value = google_cloud_run_v2_service.reader.uri }
output "writer_url" { value = google_cloud_run_v2_service.writer.uri }
output "lb_url" { value = "http://${google_compute_global_address.litestream.address}" }
output "app-image" { value = ko_build.build.image_ref }

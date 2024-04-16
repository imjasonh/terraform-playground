// TODO: state in GCS bucket

terraform {
  required_providers {
    ko  = { source = "ko-build/ko" }
    oci = { source = "chainguard-dev/oci" }
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
}

resource "google_cloud_run_v2_service" "service" {
  name     = "litestream"
  location = local.region
  ingress  = "INGRESS_TRAFFIC_ALL"

  template {
    containers {
      image = ko_build.build.image_ref
      volume_mounts {
        name       = "data"
        mount_path = "/data"
      }
    }

    /*

TODO: Uncommenting this results in
│ Error: Error waiting for Updating Service: Error code 13, message: Revision 'litestream-00009-gf9' is not ready and cannot serve traffic. Container import failed:

    containers {
      image = oci_append.append.image_ref
      args  = ["replicate", "/etc/litestream.yml"]
      volume_mounts {
        name       = "data"
        mount_path = "/data"
      }
    }
    */

    volumes {
      name = "data"
    }
  }
}

// TODO: This should be an oci_copy resource.
resource "null_resource" "crane-cp" {
  triggers = {
    bucket = google_storage_bucket.bucket.name
    image  = ko_build.build.image_ref
  }

  provisioner "local-exec" {
    command = "crane cp --platform=linux/amd64 litestream/litestream gcr.io/${local.project_id}/litestream:latest"
  }
}

resource "oci_append" "append" {
  base_image = "gcr.io/${local.project_id}/litestream:latest"
  layers = [{
    files = {
      "/etc/litestream.yml" = {
        contents = <<EOY
dbs:
  - path: /data/db.sqlite
    replicas:
      - url: gcs://${google_storage_bucket.bucket.name}/litestream
        EOY
      }
    }
  }]
}

// Allow all users to invoke the service
resource "google_cloud_run_v2_service_iam_member" "public" {
  name   = google_cloud_run_v2_service.service.name
  role   = "roles/run.invoker"
  member = "allUsers"
}

output "url" { value = google_cloud_run_v2_service.service.uri }

output "litestream-image" { value = oci_append.append.image_ref }
output "app-image" { value = ko_build.build.image_ref }

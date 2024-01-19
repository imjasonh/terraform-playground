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

data "oci_ref" "upstream" { ref = "gitea/gitea" }

resource "cosign_copy" "copy" {
  source      = "${data.oci_ref.upstream.ref}@${data.oci_ref.upstream.digest}"
  destination = "${var.region}-docker.pkg.dev/${var.project}/${google_artifact_registry_repository.repo.name}/gitea"
}

// TODO: private IP address.
resource "google_sql_database_instance" "db" {
  name             = var.name
  database_version = "POSTGRES_15"
  region           = var.region

  settings {
    tier = "db-f1-micro"
  }

  lifecycle {
    prevent_destroy = true
  }
}

resource "random_bytes" "db_user" { length = 16 }
resource "random_bytes" "db_pass" { length = 16 }

resource "google_sql_user" "db_user" {
  instance = google_sql_database_instance.db.name
  name     = random_bytes.db_user.hex
  password = random_bytes.db_user.hex
}

// TODO: grant minimal permissions to the service account.
resource "google_service_account" "sa" { account_id = "${var.name}-sa" }

// TODO: Grant the SA access to the database.

resource "google_cloud_run_v2_service" "svc" {
  name     = var.name
  location = var.region

  template {
    service_account = google_service_account.sa.email
    containers {
      image = cosign_copy.copy.copied_ref

      // TODO: Mount this in a secret!
      // The database settings are invalid: dial unix /cloudsql/jason-chainguard:us-central1:gitea/.s.PGSQL.5432: connect: connection refused
      command = [
        "/bin/sh",
        "-c",
        <<EOF
set -euo pipefail
mkdir -p /data/gitea/conf/
cat > /data/gitea/conf/app.ini <<EOF2
[database]
DB_TYPE = postgres
HOST = /cloudsql/${google_sql_database_instance.db.connection_name}
NAME = ${var.name}
USER = ${google_sql_user.db_user.name}
PASSWD = ${google_sql_user.db_user.password}
EOF2

/usr/bin/entrypoint
        EOF
      ]
      args = []

      ports {
        container_port = 3000
      }
      volume_mounts {
        name       = "cloudsql"
        mount_path = "/cloudsql"
      }
    }

    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [google_sql_database_instance.db.connection_name]
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
output "db_host" { value = google_sql_database_instance.db.ip_address[0].ip_address }

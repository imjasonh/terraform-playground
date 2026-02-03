terraform {
  required_providers {
    google = {      source  = "hashicorp/google"    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}

# Cloud SQL instance
resource "google_sql_database_instance" "step_ca" {
  name             = "step-ca-db"
  database_version = "POSTGRES_15"
  region           = var.region

  settings {
    tier = "db-f1-micro"

    ip_configuration {
      ipv4_enabled = false
      # Enable Cloud Run to connect via private IP
      private_network = google_compute_network.step_ca.id
    }

    backup_configuration {
      enabled = true
    }
  }

  deletion_protection = false
}

resource "google_sql_database" "step_ca" {
  name     = "step_ca"
  instance = google_sql_database_instance.step_ca.name
}

resource "google_sql_user" "step" {
  name     = "step"
  instance = google_sql_database_instance.step_ca.name
  password = random_password.db_password.result
}

resource "random_password" "db_password" {
  length  = 32
  special = false
}

# VPC for Cloud SQL private IP
resource "google_compute_network" "step_ca" {
  name = "step-ca-network"
}

resource "google_compute_global_address" "private_ip_address" {
  name          = "step-ca-private-ip"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = google_compute_network.step_ca.id
}

resource "google_service_networking_connection" "private_vpc_connection" {
  network                 = google_compute_network.step_ca.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_ip_address.name]
}

# VPC connector for Cloud Run
resource "google_vpc_access_connector" "connector" {
  name          = "step-ca-connector"
  region        = var.region
  network       = google_compute_network.step_ca.name
  ip_cidr_range = "10.8.0.0/28"

  depends_on = [google_service_networking_connection.private_vpc_connection]
}

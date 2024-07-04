terraform {
  required_providers {
    google = { source = "hashicorp/google" }
    ko     = { source = "ko-build/ko" }
  }
}

locals {
  project = "jason-chainguard"
  region  = "us-central1"
  name    = "iap"

  users = [
    "jason@chainguard.dev",
    "jon.johnson@chainguard.dev",
  ]
}

provider "google" {
  project = local.project
}

provider "ko" {
  repo = "gcr.io/jason-chainguard/iap"
}

module "networking" {
  source = "chainguard-dev/common/infra//modules/networking"

  name       = local.name
  project_id = local.project
  regions    = [local.region]
}

resource "google_service_account" "sa" {
  account_id   = "${local.name}-sa"
  display_name = local.name
}

module "service" {
  source = "chainguard-dev/common/infra//modules/regional-go-service"

  project_id = local.project
  name       = local.name
  regions    = module.networking.regional-networks

  ingress = "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER" // Only accessible via GCLB.
  egress  = "ALL_TRAFFIC"                            // Shouldn't send external traffic.

  service_account = google_service_account.sa.email

  containers = {
    "${local.name}" = {
      source = {
        working_dir = path.module
        importpath  = "./"
      }
      ports = [{ container_port = 8080 }]
    }
  }
  notification_channels = []
}

output "url" {
  value = module.service.uris // This URL should not be accessible directly.
}

data "google_dns_managed_zone" "top-level-zone" { name = "jason-cr-dev" }

module "gclb" {
  source = "chainguard-dev/common/infra//modules/serverless-gclb"

  name       = local.name
  project_id = local.project
  dns_zone   = data.google_dns_managed_zone.top-level-zone.name

  regions         = keys(module.networking.regional-networks)
  serving_regions = keys(module.networking.regional-networks)

  public-services = {
    "iap.imjasonh.dev" = { name = local.name }
  }

  iap = {
    oauth2_client_id     = google_iap_client.client.client_id
    oauth2_client_secret = google_iap_client.client.secret
  }
}

resource "google_iap_brand" "brand" {
  support_email     = "jason@chainguard.dev"
  application_title = "Cloud IAP protected Application"
}

resource "google_iap_client" "client" {
  display_name = "IAP Client"
  brand        = google_iap_brand.brand.name
}

resource "google_iap_web_backend_service_iam_member" "member" {
  for_each            = toset(local.users)
  web_backend_service = local.name
  role                = "roles/iap.httpsResourceAccessor"
  member              = "user:${each.key}"
}

resource "google_project_service_identity" "svc-id" {
  provider = google-beta
  project  = local.project
  service  = "iap.googleapis.com"
}

terraform {
  required_providers {
    google = { source = "hashicorp/google" }
    ko     = { source = "ko-build/ko" }
    apko   = { source = "chainguard-dev/apko" }
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

resource "ko_build" "app" {
  importpath = "github.com/jonjohnsonjr/dagdotdev/cmd/oci"
  base_image = module.base.image_ref
}

module "base" {
  source = "chainguard-dev/apko/publisher"

  target_repository = "gcr.io/jason-chainguard/iap/base"
  check_sbom        = false
  config = jsonencode({
    environment = {
      DOCKER_CONFIG = "/docker"
    }
    contents = {
      repositories = [
        "https://packages.wolfi.dev/os",
        "https://packages.cgr.dev/extras",
      ]
      keyring = [
        "https://packages.wolfi.dev/os/wolfi-signing.rsa.pub",
        "https://packages.cgr.dev/extras/chainguard-extras.rsa.pub",
      ]
      packages = [
        "wolfi-baselayout",
        "chainctl",
        //"docker-credential-cgr",
      ]
    }
    archs = ["amd64"]
  })
}

module "service" {
  source = "chainguard-dev/common/infra//modules/regional-service"

  project_id = local.project
  name       = local.name
  regions    = module.networking.regional-networks

  ingress = "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER" // Only accessible via GCLB.
  egress  = "ALL_TRAFFIC"                            // Shouldn't send external traffic.

  service_account = google_service_account.sa.email

  containers = {
    "${local.name}" = {
      image = ko_build.app.image_ref
      ports = [{ container_port = 8080 }]
      environment = {
        //IDENTITY_UID = chainguard_identity.identity.uid
        AUTH = "keychain"
      }
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

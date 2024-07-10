terraform {
  required_providers {
    google     = { source = "hashicorp/google" }
    ko         = { source = "ko-build/ko" }
    apko       = { source = "chainguard-dev/apko" }
    chainguard = { source = "chainguard-dev/chainguard" }
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

module "dagdotdev" {
  source = "chainguard-dev/apko/publisher"

  target_repository = "gcr.io/jason-chainguard/iap"
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
        "docker-credential-cgr",
        "dagdotdev",
      ]
    }
    archs = ["amd64"]
    cmd   = "/usr/bin/dagdotdev oci"
  })
}

module "service" {
  source = "chainguard-dev/common/infra//modules/regional-service"

  project_id = local.project
  name       = local.name
  regions    = module.networking.regional-networks

  ingress = "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER" // Only accessible via GCLB.
  egress  = "PRIVATE_RANGES_ONLY"                    // Talks to the internet.

  service_account = google_service_account.sa.email

  containers = {
    "${local.name}" = {
      image = module.dagdotdev.image_ref
      ports = [{ container_port = 8080 }]
      environment = {
        IDENTITY_UID = chainguard_identity.identity.id
        AUTH         = "keychain"
        CACHE_BUCKET = "/gcs"
      }
      volume_mounts = [{
        name       = "gcs"
        mount_path = "/gcs"
      }]
    }
  }

  volumes = [{
    name = "gcs"
    gcs = {
      bucket    = google_storage_bucket.bucket.name
      read_only = false
    }
  }]

  notification_channels = []
}

resource "google_storage_bucket" "bucket" {
  name     = "jason-dagdotdev"
  location = local.region
}

resource "google_storage_bucket_iam_member" "bucket" {
  bucket = google_storage_bucket.bucket.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.sa.email}"
}

resource "chainguard_identity" "identity" {
  parent_id = data.chainguard_group.group.id
  name      = "iap dag.dev"
  claim_match {
    issuer  = "https://accounts.google.com"
    subject = google_service_account.sa.email
  }
}

data "chainguard_group" "group" { name = "imjasonh.dev" }
data "chainguard_role" "puller" { name = "registry.pull" }

resource "chainguard_rolebinding" "puller" {
  group    = data.chainguard_group.group.id
  role     = data.chainguard_role.puller.items[0].id
  identity = chainguard_identity.identity.id
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

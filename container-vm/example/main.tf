locals {
  name       = "container-vm"
  project_id = "jason-chainguard"
  region     = "us-east4"
  zone       = "${local.region}-a"
}

provider "google" {
  project = local.project_id
}

resource "google_service_account" "sa" {
  account_id   = local.name
}

resource "google_artifact_registry_repository" "container_images" {
  location      = local.region
  repository_id = local.name
  format        = "DOCKER"
}

resource "google_artifact_registry_repository_iam_member" "sa_reader" {
  location   = google_artifact_registry_repository.container_images.location
  repository = google_artifact_registry_repository.container_images.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.sa.email}"
}

resource "google_project_iam_member" "observability_roles" {
  for_each = toset([
    "roles/logging.logWriter",
    "roles/monitoring.metricWriter",
    "roles/cloudtrace.agent",
    "roles/cloudprofiler.agent",
  ])
  project = local.project_id
  role    = each.key
  member  = "serviceAccount:${google_service_account.sa.email}"
}

module "container-vm" {
  source = "./container-vm"

  project_id = local.project_id
  region     = local.region

  containers = {
    "nginx" = {
      image = "${local.region}-docker.pkg.dev/${local.project_id}/${local.name}/nginx@sha256:553f64aecdc31b5bf944521731cd70e35da4faed96b2b7548a3d8e2598c52a42"
      ports = ["80:80"]
    }
  }

  service_account_email = google_service_account.sa.email

  network    = "default"
  subnetwork = "default"
}

resource "google_compute_instance_from_template" "instance" {
  name = local.name
  zone = "${local.region}-a"
  tags = ["allow-health-check"]

  source_instance_template = module.container-vm.instance_template_self_link
}

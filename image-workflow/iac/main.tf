terraform {
  required_providers {
    cosign     = { source = "chainguard-dev/cosign" }
    ko         = { source = "ko-build/ko" }
    google     = { source = "hashicorp/google" }
    chainguard = { source = "chainguard/chainguard" }
  }
}

provider "google" {
  project = var.project_id
}

resource "google_service_account" "image-workflow" {
  account_id = "${var.name}-image-workflow"
}

resource "google_secret_manager_secret" "gh-pat" {
  project   = var.project_id
  secret_id = "${var.name}-github-pat"
  replication {
    automatic = true
  }
}

resource "google_secret_manager_secret_version" "initial-secret-version" {
  secret = google_secret_manager_secret.gh-pat.id

  secret_data = "you need to populate the secret."

  lifecycle {
    ignore_changes = [
      # This is populated after everything is up.
      secret_data
    ]
  }
}

resource "google_secret_manager_secret_iam_member" "grant-secret-access" {
  secret_id = google_secret_manager_secret.gh-pat.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.image-workflow.email}"
}

data "cosign_verify" "base-image" {
  image = "cgr.dev/chainguard/static:latest-glibc"

  policy = jsonencode({
    apiVersion = "policy.sigstore.dev/v1beta1"
    kind       = "ClusterImagePolicy"
    metadata = {
      name = "chainguard-images-are-signed"
    }
    spec = {
      images = [{
        glob = "cgr.dev/**"
      }]
      authorities = [{
        keyless = {
          url = "https://fulcio.sigstore.dev"
          identities = [{
            issuer  = "https://token.actions.githubusercontent.com"
            subject = "https://github.com/chainguard-images/images/.github/workflows/release.yaml@refs/heads/main"
          }]
        }
        ctlog = {
          url = "https://rekor.sigstore.dev"
        }
      }]
    }
  })
}

resource "ko_build" "image" {
  importpath  = "github.com/imjasonh/terraform-playground/image-workflow/cmd/app"
  working_dir = path.module
}

resource "google_cloud_run_service" "image-workflow" {
  name     = "${var.name}-image-workflow"
  location = var.location

  template {
    spec {
      service_account_name = google_service_account.image-workflow.email
      containers {
        image = ko_build.image.image_ref
        env {
          name  = "ISSUER_URL"
          value = "https://issuer.${var.env}"
        }
        env {
          name  = "GROUP"
          value = var.group
        }
        env {
          name  = "GITHUB_ORG"
          value = var.github_org
        }
        env {
          name  = "GITHUB_REPO"
          value = var.github_repo
        }
        env {
          name  = "GITHUB_WORKFLOW_ID"
          value = var.github_workflow_id
        }
        env {
          name = "GITHUB_TOKEN"
          value_from {
            secret_key_ref {
              name = google_secret_manager_secret.gh-pat.secret_id
              key  = "latest"
            }
          }
        }
      }
    }
  }
}

data "google_iam_policy" "noauth" {
  binding {
    role = "roles/run.invoker"
    members = [
      "allUsers",
    ]
  }
}

resource "google_cloud_run_service_iam_policy" "noauth" {
  location = google_cloud_run_service.image-workflow.location
  service  = google_cloud_run_service.image-workflow.name

  policy_data = data.google_iam_policy.noauth.policy_data
}

# Create a subscription to notify the Cloud Run service on changes under the root group.
resource "chainguard_subscription" "subscription" {
  parent_id = var.group
  sink      = google_cloud_run_service.image-workflow.status[0].url
}

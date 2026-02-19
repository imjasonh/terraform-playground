resource "google_service_account" "risk_scorer" {
  project    = var.project_id
  account_id = "driftlessaf-pr-risk-scorer"
}

# Grant Vertex AI permissions for risk assessment (Claude API access)
resource "google_project_iam_member" "risk_scorer_vertex_ai" {
  project = var.project_id
  role    = "roles/aiplatform.user"
  member  = "serviceAccount:${google_service_account.risk_scorer.email}"
}

module "pr-risk-scorer" {
  source = "chainguard-dev/common/infra//modules/regional-go-reconciler"

  project_id      = var.project_id
  name            = "pr-risk-scorer"
  regions         = module.networking.regional-networks
  service_account = google_service_account.risk_scorer.email

  # Allow direct internet access for GitHub API calls
  egress = "PRIVATE_RANGES_ONLY"

  containers = {
    "risk-scorer" = {
      source = {
        working_dir = path.module
        importpath  = "./cmd/pr-risk-scorer"
      }
      ports = [{ container_port = 8080 }]
      env = [
        { name = "GITHUB_APP_ID", value = var.github_app_id },
        {
          name = "GITHUB_PRIVATE_KEY"
          value_source = {
            secret_key_ref = {
              secret  = google_secret_manager_secret.github_app_key.secret_id
              version = "latest"
            }
          }
        },
        { name = "GCP_PROJECT_ID", value = var.project_id },
        { name = "GCP_REGION", value = var.risk_scorer_region },
        { name = "CLAUDE_MODEL", value = var.risk_scorer_model },
      ]
    }
  }

  team                  = var.team
  notification_channels = []
  deletion_protection   = var.deletion_protection
}

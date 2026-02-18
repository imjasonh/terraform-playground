resource "google_service_account" "risk_scorer" {
  project    = var.project_id
  account_id = "driftlessaf-pr-risk-scorer"
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
      ]
    }
  }

  team                  = var.team
  notification_channels = []
  deletion_protection   = var.deletion_protection
}

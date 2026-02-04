resource "google_service_account" "reconciler" {
  project    = var.project_id
  account_id = "driftlessaf-pr-reconciler"
}

module "pr-reconciler" {
  source = "chainguard-dev/common/infra//modules/regional-go-reconciler"

  project_id      = var.project_id
  name            = "pr-logger"
  regions         = module.networking.regional-networks
  service_account = google_service_account.reconciler.email

  # Allow direct internet access for GitHub API calls
  egress = "PRIVATE_RANGES_ONLY"

  containers = {
    "reconciler" = {
      source = {
        working_dir = path.module
        importpath  = "./cmd/pr-reconciler"
      }
      ports = [{ container_port = 8080 }]
      env = concat([
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
      ],
      # CI Fixer configuration (only when enabled)
      var.enable_ci_fixer ? [
        { name = "ENABLE_CI_FIXER", value = "true" },
        { name = "GCP_PROJECT_ID", value = var.project_id },
        { name = "GCP_REGION", value = var.ci_fixer_region },
        { name = "CLAUDE_MODEL", value = var.ci_fixer_model },
        { name = "MAX_TURNS", value = tostring(var.ci_fixer_max_turns) },
        { name = "CI_FIXER_LABEL", value = var.ci_fixer_label },
      ] : [])
    }
  }

  team                  = var.team
  notification_channels = []
  deletion_protection   = var.deletion_protection
}

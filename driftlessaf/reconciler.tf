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

  containers = {
    "reconciler" = {
      source = {
        working_dir = path.module
        importpath  = "./cmd/pr-reconciler"
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
  deletion_protection   = false
}

module "networking" {
  source = "chainguard-dev/common/infra//modules/networking"

  project_id = var.project_id
  name       = "driftlessaf-networking"
  regions    = ["us-central1", "us-east4"]

  hosted_zone_logging_enabled = false

  team = var.team
}

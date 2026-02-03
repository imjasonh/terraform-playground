module "github-events" {
  source = "chainguard-dev/common/infra//modules/github-events"

  project_id = var.project_id
  name       = "driftless-github-events"
  regions    = module.networking.regional-networks
  ingress    = module.cloudevent-broker.ingress

  github_organizations = "imjasonh"

  secret_version_adder = var.secret_version_adder

  team                  = var.team
  notification_channels = []
  deletion_protection   = false
}

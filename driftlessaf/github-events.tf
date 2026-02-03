module "github-events" {
  source = "chainguard-dev/common/infra//modules/github-events"

  project_id = var.project_id
  name       = "driftless-github-events"
  regions    = module.networking.regional-networks
  ingress    = module.cloudevent-broker.ingress

  # Note: github_organizations only works for org-owned repos, not user repos
  # github_organizations = "imjasonh"

  secret_version_adder = var.secret_version_adder

  # Allow public access for GitHub webhooks
  # TODO: Put this behind GCLB
  service-ingress = "INGRESS_TRAFFIC_ALL"

  team                  = var.team
  notification_channels = []
  deletion_protection   = false
}

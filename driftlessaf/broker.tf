module "cloudevent-broker" {
  source = "chainguard-dev/common/infra//modules/cloudevent-broker"

  project_id = var.project_id
  name       = "driftlessaf"
  regions    = module.networking.regional-networks

  team                  = var.team
  notification_channels = []
  deletion_protection   = var.deletion_protection
}

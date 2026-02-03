module "github-events-recorder" {
  source = "chainguard-dev/common/infra//modules/cloudevent-recorder"
  method = "gcs"

  project_id = var.project_id
  name       = "github-events-recorder"
  regions    = module.networking.regional-networks
  broker     = module.cloudevent-broker.broker

  # Identity applying terraform (needed for BQ DTS permissions)
  provisioner = "user:jason@chainguard.dev"

  # BigQuery location and retention
  location         = "US"
  retention-period = 30 # days

  # Record all GitHub event types using schemas from github-events module
  # Must set ignore_unknown_values since GitHub payloads have more fields than the schema
  types                 = module.github-events.recorder-schemas
  ignore_unknown_values = true

  team                  = var.team
  notification_channels = []
  deletion_protection   = false
}

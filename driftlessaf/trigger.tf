module "pr-trigger" {
  for_each = module.networking.regional-networks

  source = "chainguard-dev/common/infra//modules/cloudevent-trigger"

  project_id = var.project_id
  name       = "pr-events"
  broker     = module.cloudevent-broker.broker[each.key]

  filter = {
    "type" = "dev.chainguard.github.pull_request"
  }

  private-service = {
    name   = module.pr-reconciler.receiver.name
    region = each.key
  }

  team                  = var.team
  notification_channels = []
}

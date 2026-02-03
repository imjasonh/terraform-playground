module "pr-workqueue" {
  source = "chainguard-dev/common/infra//modules/cloudevents-workqueue"

  project_id = var.project_id
  name       = "pr-events"
  regions    = module.networking.regional-networks
  broker     = module.cloudevent-broker.broker

  # Empty filter matches all events that have pullrequesturl extension
  # This includes pull_request, issue_comment on PRs, pull_request_review, etc.
  filters = [{}]

  extension_key = "pullrequesturl"

  workqueue = {
    name = module.pr-reconciler.receiver.name
  }

  team                  = var.team
  notification_channels = []
  deletion_protection   = false
}

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
  deletion_protection   = var.deletion_protection
}

# Separate workqueue for check_run events (CI completion notifications).
#
# Why this is needed:
# The github-events module does NOT set the pullrequesturl extension for check_run
# events (the code is commented out in terraform-infra-common). This means check_run
# events don't match the pr-workqueue filter above.
#
# For the CI fixer to work, we need to be notified when CI checks complete so we can
# re-evaluate whether to attempt a fix. This workqueue listens for check_run events
# directly and the reconciler extracts the PR URL from the event payload.
#
# See: https://github.com/chainguard-dev/terraform-infra-common/blob/main/modules/github-events/internal/trampoline/server.go
module "check-run-workqueue" {
  source = "chainguard-dev/common/infra//modules/cloudevents-workqueue"

  project_id = var.project_id
  name       = "check-run-events"
  regions    = module.networking.regional-networks
  broker     = module.cloudevent-broker.broker

  # Filter for check_run events with "completed" action
  filters = [{
    "type"   = "dev.chainguard.github.check_run"
    "action" = "completed"
  }]

  # Use subject (repo full name like "owner/repo") as the key.
  # The trampoline sets event.SetSubject(repoFullName) which becomes ce-subject attribute.
  # Note: "repo" extension doesn't exist - the trampoline only sets subject, not a repo extension.
  extension_key = "subject"

  workqueue = {
    name = module.pr-reconciler.receiver.name
  }

  team                  = var.team
  notification_channels = []
  deletion_protection   = var.deletion_protection
}

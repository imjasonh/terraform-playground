# Dashboards for the PR reconciler infrastructure

module "pr-reconciler-dashboard" {
  source = "chainguard-dev/common/infra//modules/dashboard/reconciler"

  project_id = var.project_id
  name       = "pr-logger"

  # Workqueue configuration (should match reconciler module settings)
  max_retry       = 100
  concurrent_work = 20

  # Enable GitHub API metrics section since we call GitHub
  sections = {
    github = true
  }

  notification_channels = []

  labels = {
    team = var.team
  }
}

module "pr-workqueue-dashboard" {
  source = "chainguard-dev/common/infra//modules/dashboard/workqueue"

  name            = "pr-logger-wq"
  concurrent_work = 20
  max_retry       = 100

  labels = {
    team = var.team
  }
}

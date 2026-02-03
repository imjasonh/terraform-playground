# Driftlessaf Infrastructure Deployment Summary

This document summarizes the deployment of a GitHub PR reconciler using Chainguard's terraform-infra-common modules.

## Architecture

The system receives GitHub webhook events, routes them through a CloudEvents broker, and processes PR events in a reconciler service that logs PR details.

```
GitHub Webhooks → github-events → cloudevent-broker → cloudevents-workqueue → pr-reconciler
```

## Components Deployed

- **Networking**: VPC with regional subnets in us-central1 and us-east4
- **CloudEvent Broker**: Regional Pub/Sub topics for event routing
- **GitHub Events**: Webhook receiver that converts GitHub webhooks to CloudEvents with extensions like `pullrequesturl`
- **CloudEvents Workqueue**: Subscribes to all events with `pullrequesturl` extension (pull_request, issue_comment on PRs, PR reviews) and enqueues them for processing
- **PR Reconciler**: Regional Go gRPC service that processes PR events via workqueue, logging PR details including title, state, labels, reviews, status checks, changed files, and latest comments

## Issues Encountered and Resolutions

### 1. Module Source Confusion

**Problem**: The original plan referenced `driftlessaf/reconcilers/infra//modules/regional-go-reconciler` which doesn't exist.

**Resolution**: Used the correct source `chainguard-dev/common/infra//modules/regional-go-reconciler`.

### 2. Missing Required Variables

**Problem**: Several modules require a `team` variable that wasn't documented in the plan.

**Resolution**: Added `team` variable to all modules (cloudevent-broker, github-events, regional-go-reconciler, cloudevent-trigger).

### 3. Trigger Output Mismatch

**Problem**: Tried to use `module.pr-reconciler.names[each.key]` but the regional-go-reconciler module doesn't expose a `names` output.

**Resolution**: Used `module.pr-reconciler.receiver.name` which is the workqueue receiver that triggers should connect to.

### 4. Secret Volume Mount Not Supported

**Problem**: The `regional-go-reconciler` module's `volumes` variable only supports `empty_dir` and `csi` types, not `secret` volumes. The secret mount at `/secrets/github/private-key.pem` was empty.

**Resolution**: Changed from volume mount to environment variable with `secret_key_ref`:
```hcl
env = [
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
```

Updated Go code to read `GITHUB_PRIVATE_KEY` from environment instead of file.

### 5. Private DNS Zone Logging

**Problem**: 
```
Error: Invalid value for 'Logging may only be enabled for PUBLIC ManageZones.
```

The networking module tried to enable logging on private DNS zones, which GCP doesn't support.

**Resolution**: Added `hosted_zone_logging_enabled = false` to the networking module.

### 6. Pre-existing GCP Resources

**Problem**: Multiple resources already existed from previous deployments:
- Cloud Run services named `driftlessaf`
- DNS policy `dns-logging-policy`
- DNS managed zones
- Serverless VPC addresses

**Resolution**: 
- Deleted existing Cloud Run services: `gcloud run services delete driftlessaf --region=<region>`
- Imported existing DNS resources: `terraform import 'module.networking.google_dns_policy.dns_logging_policy' jason-chainguard/dns-logging-policy`
- Removed stuck subnets from state: `terraform state rm 'module.networking.google_compute_subnetwork.regional["us-central1"]'`

### 7. Orphaned Serverless Addresses

**Problem**: Subnets couldn't be modified because they were held by serverless IP addresses from deleted Cloud Run services.

**Resolution**: Changed the networking module name from `driftlessaf` to `driftlessaf-networking` to create new subnets with different names, avoiding the locked subnets.

### 8. Deletion Protection

**Problem**: Couldn't destroy/recreate resources due to deletion protection.

**Resolution**: Added `deletion_protection = false` to all modules:
- `broker.tf`
- `github-events.tf`
- `reconciler.tf`

### 9. Multi-Installation Support

**Problem**: Original design hardcoded a single `github_installation_id`, limiting the app to one org.

**Resolution**: Refactored Go code to:
1. Authenticate as the GitHub App using `ghinstallation.NewAppsTransport`
2. Dynamically look up installations per repo using `Apps.FindRepositoryInstallation(owner, repo)`
3. Cache GitHub clients per installation ID

This allows the same GitHub App to be installed on multiple organizations.

### 10. github_organizations Filter Doesn't Work for User Repos

**Problem**: The `github_organizations` filter in the github-events module only works for organization-owned repositories. For user-owned repos (like `imjasonh/terraform-playground`), the Organization field in GitHub events is empty, so the filter rejects them with "ignoring event from repository '' due to non-matching org".

**Resolution**: Commented out the `github_organizations` filter to allow events from all repositories where the GitHub App is installed.

### 11. CloudEvent Trigger vs CloudEvents Workqueue

**Problem**: Initially used the `cloudevent-trigger` module to route events to the workqueue receiver. This module pushes HTTP POST messages from Pub/Sub, but the workqueue receiver expects gRPC calls, resulting in 404 errors.

**Resolution**: Replaced `cloudevent-trigger` with `cloudevents-workqueue` module, which is specifically designed to:
1. Subscribe to CloudEvents from the broker
2. Extract a key from a CloudEvent extension (e.g., `pullrequesturl`)
3. Enqueue work items to the workqueue via gRPC

```hcl
module "pr-workqueue" {
  source = "chainguard-dev/common/infra//modules/cloudevents-workqueue"
  
  # Empty filter matches all events that have pullrequesturl extension
  # This includes pull_request, issue_comment on PRs, pull_request_review, etc.
  filters = [{}]
  
  extension_key = "pullrequesturl"
  
  workqueue = {
    name = module.pr-reconciler.receiver.name
  }
}
```

### 12. httpmetrics.ServeMetrics() Blocks

**Problem**: The gRPC server never started. Requests would arrive at Cloud Run but the Process handler was never called. After extensive debugging, discovered that `httpmetrics.ServeMetrics()` was being called directly instead of in a goroutine, causing it to block forever.

**Symptoms**:
- Startup logs appeared but "Starting gRPC server" never logged
- HTTP requests completed with 200 status after exactly 300 seconds (Cloud Run timeout)
- No logs from the Process handler

**Resolution**: Run ServeMetrics in a goroutine:
```go
// WRONG - blocks forever
httpmetrics.ServeMetrics()

// CORRECT - runs in background
go httpmetrics.ServeMetrics()
```

All example code in terraform-infra-common uses `go httpmetrics.ServeMetrics()`.

### 13. VPC Egress Blocks External API Access

**Problem**: After fixing the gRPC server startup, the reconciler could receive requests but failed to reach the GitHub API with timeout errors:
```
dial tcp 140.82.112.5:443: i/o timeout
```

**Root Cause**: The reconciler was configured with `vpc-access-egress: all-traffic`, which routes ALL outbound traffic through the VPC. The VPC's private DNS zones redirect `*.run.app` to `private.googleapis.com` for internal Cloud Run access, but there's no NAT gateway for external internet access.

**Resolution**: Set the reconciler's egress to `PRIVATE_RANGES_ONLY`:
```hcl
module "pr-reconciler" {
  source = "chainguard-dev/common/infra//modules/regional-go-reconciler"
  
  # Allow direct internet access for GitHub API calls
  egress = "PRIVATE_RANGES_ONLY"
  
  # ...
}
```

This routes only private IP traffic (10.x.x.x, 172.16.x.x, 192.168.x.x) through the VPC, while allowing direct internet access for public APIs like GitHub.

## Secrets to Populate

After `terraform apply`, populate these secrets:

```bash
# GitHub App private key
gcloud secrets versions add driftlessaf-github-app-private-key \
  --project=jason-chainguard \
  --data-file=path/to/private-key.pem

# Webhook secret (generate random value)
openssl rand -hex 32 | gcloud secrets versions add driftlessaf-webhook-secret \
  --project=jason-chainguard \
  --data-file=-
```

Configure the GitHub App's webhook settings with:
- **Webhook URL**: From `terraform output github_webhook_urls`
- **Webhook Secret**: The value generated above
- **Events**: Enable "Pull requests" (and optionally "Issues", "Issue comments")

### 14. Empty Filters Create Zero Triggers

**Problem**: When trying to allow all PR-related events (not just `pull_request`), commented out the `filters` array. But this resulted in no events being processed at all.

**Root Cause**: The `cloudevents-workqueue` module creates triggers using:
```hcl
for_each = {
  for pair in setproduct(keys(var.regions), range(length(var.filters))) :
  ...
}
```

When `filters = []` (empty array), `range(length(var.filters)) = range(0) = []`, and `setproduct(regions, []) = []`. So **zero triggers are created**, meaning no Pub/Sub subscriptions exist to deliver events.

**Resolution**: Use an empty filter `[{}]` to match all events:
```hcl
# WRONG - creates zero triggers
filters = []

# ALSO WRONG - commented out means filters defaults to [], same problem
# filters = [{ "type" = "..." }]

# CORRECT - creates triggers that match ALL events
filters = [{}]
```

The `extension_key = "pullrequesturl"` still applies via `filter_has_attributes`, so only events with that extension are delivered.

### 15. Issue Comments on PRs Set pullrequesturl Extension

**Problem**: Initially thought PR comments (`issue_comment` events) weren't being processed because they lacked the `pullrequesturl` extension.

**Investigation**: Created a temporary Pub/Sub subscription without filters to inspect actual messages. Found that the trampoline DOES set `ce-pullrequesturl` for issue_comment events on PRs:
```json
{
  "ce-type": "dev.chainguard.github.issue_comment",
  "ce-pullrequesturl": "https://github.com/imjasonh/terraform-playground/pull/1"
}
```

The trampoline checks if `issue.pull_request` is non-null in the webhook payload, and if so, extracts the PR URL.

**Resolution**: Use `filters = [{}]` to accept all events with `pullrequesturl`. This includes:
- `pull_request` events
- `issue_comment` events on PRs
- `pull_request_review` events
- `pull_request_review_comment` events

## Lessons Learned

1. **Read module variable definitions carefully** - especially for complex nested types like `volumes` and `containers`.

2. **Check module outputs** - don't assume output names; verify with the actual module source.

3. **Secrets as env vars vs volume mounts** - not all modules support secret volumes; env vars with `secret_key_ref` are more universally supported.

4. **GCP resource cleanup can be tricky** - serverless addresses can remain locked even after deleting the services that created them.

5. **Import existing resources** - when resources already exist, `terraform import` is often cleaner than trying to delete and recreate.

6. **Private DNS zones don't support logging** - use DNS Server Policies for logging instead.

7. **Plan for multi-tenancy early** - designing for multiple GitHub App installations from the start avoids refactoring later.

8. **CloudEvent routing is not trivial** - the `cloudevent-trigger` module pushes HTTP, while `cloudevents-workqueue` properly bridges to gRPC workqueues. Choose the right module for your use case.

9. **Always run httpmetrics.ServeMetrics() in a goroutine** - it blocks! Check example code in terraform-infra-common for the correct pattern.

10. **VPC egress settings matter** - `ALL_TRAFFIC` routes everything through the VPC, breaking external API access unless you have a NAT gateway. Use `PRIVATE_RANGES_ONLY` when your service needs to call external APIs.

11. **Add debug logging early** - when things don't work, having logs at key points (gRPC handler entry, before/after network calls) saves hours of debugging.

12. **Cloud Run request logs show latency** - if requests consistently hit the timeout (300s), something is blocking. Check if the handler is even being called.

13. **Empty filter arrays create zero resources** - in Terraform `for_each` with `setproduct(list, range(length([])))`, an empty list results in zero iterations. Use `[{}]` instead of `[]` for "match all" filters.

14. **Debug Pub/Sub by creating unfiltered subscriptions** - when events seem to disappear, create a temporary subscription without filters to inspect what's actually being published. Remember to delete it after debugging.

15. **CloudEvent extensions are set correctly for PR-related events** - the github-events trampoline sets `pullrequesturl` for `issue_comment`, `pull_request_review`, and `pull_request_review_comment` events when they're on PRs, not just for `pull_request` events.

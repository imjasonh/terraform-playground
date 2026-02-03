# GitHub PR Reconciler Plan

## Overview

Deploy an example GitHub PR reconciler that logs PR events from `imjasonh/terraform-playground`. The system will:

1. Receive GitHub webhook events via a CloudEvents broker
2. Route PR events to a workqueue
3. Process events in a regional Go reconciler service that logs parsed PR details

## Architecture

```
GitHub (terraform-playground repo)
    │
    │ Webhooks (PR events)
    ▼
┌─────────────────────────────────────┐
│  github-events (trampoline)         │
│  - Validates webhook signature      │
│  - Converts to CloudEvents          │
└─────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────┐
│  cloudevent-broker                  │
│  - Regional Pub/Sub topics          │
│  - us-central1, us-east4            │
└─────────────────────────────────────┘
    │
    │ cloudevent-trigger (filters PR events)
    ▼
┌─────────────────────────────────────┐
│  cloudevents-workqueue              │
│  - Extracts PR URL as key           │
│  - Deduplicates concurrent events   │
└─────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────┐
│  regional-go-reconciler             │
│  - Workqueue dispatcher             │
│  - PR reconciler service            │
│  - Logs PR details via GitHub API   │
└─────────────────────────────────────┘
```

## GitHub App Authentication

### Why Not Octo STS?

The `go-driftlessaf` library uses [Octo STS](https://github.com/octo-sts/app) which is a token exchange service that:
- Requires deploying and managing the Octo STS infrastructure
- Exchanges workload identity tokens for GitHub installation tokens
- Adds an extra hop in the authentication flow

### Direct GitHub App Approach

For this example, we'll use direct GitHub App authentication via [`ghinstallation`](https://github.com/bradleyfalzon/ghinstallation):

```go
import (
    "github.com/bradleyfalzon/ghinstallation/v2"
    "github.com/google/go-github/v68/github"
)

// Create transport from App ID, Installation ID, and private key
itr, err := ghinstallation.New(http.DefaultTransport, appID, installationID, privateKey)
client := github.NewClient(&http.Client{Transport: itr})
```

This requires:
- GitHub App ID (from app settings)
- Installation ID (from installing the app on the repo)
- Private key (stored in Google Secret Manager)

The reconciler service will read these from environment variables / secrets at runtime.

## Components to Deploy

### 1. Networking (`networking.tf`)

```hcl
module "networking" {
  source = "chainguard-dev/common/infra//modules/networking"

  project_id = var.project_id
  name       = "driftlessaf"
  regions    = ["us-central1", "us-east4"]
}
```

### 2. CloudEvents Broker (`broker.tf`)

```hcl
module "cloudevent-broker" {
  source = "chainguard-dev/common/infra//modules/cloudevent-broker"

  project_id = var.project_id
  name       = "driftlessaf"
  regions    = module.networking.regional-networks

  notification_channels = []
}
```

### 3. GitHub Events Receiver (`github-events.tf`)

```hcl
module "github-events" {
  source = "chainguard-dev/common/infra//modules/github-events"

  project_id = var.project_id
  name       = "driftlessaf"
  regions    = module.networking.regional-networks
  broker     = module.cloudevent-broker.broker

  # Filter to only accept events from your org/repo
  github_organizations = "imjasonh"

  notification_channels = []
}
```

### 4. PR Reconciler (`reconciler.tf`)

```hcl
module "pr-reconciler" {
  source = "driftlessaf/reconcilers/infra//modules/regional-go-reconciler"

  project_id = var.project_id
  name       = "pr-logger"
  regions    = module.networking.regional-networks

  service_account = google_service_account.reconciler.email

  containers = {
    "reconciler" = {
      source = {
        working_dir = "${path.module}/cmd/pr-reconciler"
        importpath  = "."
      }
      ports = [{ container_port = 8080 }]
      env = [
        { name = "GITHUB_APP_ID", value = var.github_app_id },
        { name = "GITHUB_INSTALLATION_ID", value = var.github_installation_id },
      ]
      volume_mounts = [{
        name       = "github-app-key"
        mount_path = "/secrets/github"
        read_only  = true
      }]
    }
  }

  volumes = [{
    name = "github-app-key"
    secret = {
      secret_name = google_secret_manager_secret.github_app_key.secret_id
    }
  }]

  notification_channels = []
}
```

### 5. CloudEvents Trigger (`trigger.tf`)

```hcl
module "pr-trigger" {
  source = "chainguard-dev/common/infra//modules/cloudevent-trigger"

  project_id = var.project_id
  name       = "pr-events"
  regions    = module.networking.regional-networks
  broker     = module.cloudevent-broker.broker

  # Filter for PR events only
  filter = {
    "type" = "dev.chainguard.github.pull_request"
  }

  private-service = {
    name   = module.pr-reconciler.name
    region = keys(module.networking.regional-networks)[0]
  }

  notification_channels = []
}
```

### 6. Secrets (`secrets.tf`)

```hcl
resource "google_secret_manager_secret" "github_app_key" {
  project   = var.project_id
  secret_id = "github-app-private-key"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret" "webhook_secret" {
  project   = var.project_id
  secret_id = "github-webhook-secret"

  replication {
    auto {}
  }
}
```

## Go Service Implementation

### `cmd/pr-reconciler/main.go`

```go
package main

import (
    "context"
    "log/slog"
    "net/http"
    "os"
    "strconv"

    "github.com/bradleyfalzon/ghinstallation/v2"
    "github.com/chainguard-dev/clog"
    "github.com/driftlessaf/go-driftlessaf/reconcilers/githubreconciler"
    "github.com/driftlessaf/go-driftlessaf/workqueue"
    "github.com/google/go-github/v68/github"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
)

func main() {
    ctx := context.Background()
    log := clog.FromContext(ctx)

    // Read GitHub App credentials
    appID, _ := strconv.ParseInt(os.Getenv("GITHUB_APP_ID"), 10, 64)
    installID, _ := strconv.ParseInt(os.Getenv("GITHUB_INSTALLATION_ID"), 10, 64)
    privateKey, _ := os.ReadFile("/secrets/github/private-key.pem")

    // Create GitHub client with App authentication
    itr, err := ghinstallation.New(http.DefaultTransport, appID, installID, privateKey)
    if err != nil {
        log.Error("failed to create GitHub transport", "error", err)
        os.Exit(1)
    }
    ghClient := github.NewClient(&http.Client{Transport: itr})

    // Create reconciler
    reconciler := &PRReconciler{
        gh:  ghClient,
        log: log,
    }

    // Start gRPC server for workqueue
    workqueue.ServeReconciler(ctx, reconciler)
}

type PRReconciler struct {
    workqueue.UnimplementedWorkqueueServiceServer
    gh  *github.Client
    log *slog.Logger
}

func (r *PRReconciler) Process(ctx context.Context, req *workqueue.ProcessRequest) (*workqueue.ProcessResponse, error) {
    log := r.log.With("key", req.Key)

    // Parse the PR URL
    resource, err := githubreconciler.ParseURL(req.Key)
    if err != nil {
        log.Error("failed to parse PR URL", "error", err)
        return nil, status.Error(codes.InvalidArgument, err.Error())
    }

    if resource.Type != githubreconciler.PullRequest {
        log.Warn("received non-PR event", "type", resource.Type)
        return &workqueue.ProcessResponse{}, nil
    }

    // Fetch PR details from GitHub API
    pr, _, err := r.gh.PullRequests.Get(ctx, resource.Owner, resource.Repo, resource.Number)
    if err != nil {
        log.Error("failed to fetch PR", "error", err)
        return nil, err
    }

    // Log basic PR details
    log.Info("processing PR",
        "owner", resource.Owner,
        "repo", resource.Repo,
        "number", resource.Number,
        "title", pr.GetTitle(),
        "state", pr.GetState(),
        "author", pr.GetUser().GetLogin(),
        "head", pr.GetHead().GetRef(),
        "base", pr.GetBase().GetRef(),
        "mergeable", pr.GetMergeable(),
        "additions", pr.GetAdditions(),
        "deletions", pr.GetDeletions(),
        "changed_files", pr.GetChangedFiles(),
    )

    // Log labels
    var labelNames []string
    for _, label := range pr.Labels {
        labelNames = append(labelNames, label.GetName())
    }
    log.Info("PR labels", "labels", labelNames)

    // Fetch and log reviews
    reviews, _, err := r.gh.PullRequests.ListReviews(ctx, resource.Owner, resource.Repo, resource.Number, nil)
    if err != nil {
        log.Warn("failed to fetch reviews", "error", err)
    } else {
        for _, review := range reviews {
            log.Info("PR review",
                "reviewer", review.GetUser().GetLogin(),
                "state", review.GetState(),
                "submitted_at", review.GetSubmittedAt(),
            )
        }
    }

    // Fetch and log CI status (combined status + check runs)
    combinedStatus, _, err := r.gh.Repositories.GetCombinedStatus(ctx, resource.Owner, resource.Repo, pr.GetHead().GetSHA(), nil)
    if err != nil {
        log.Warn("failed to fetch combined status", "error", err)
    } else {
        log.Info("PR combined status",
            "state", combinedStatus.GetState(),
            "total_count", combinedStatus.GetTotalCount(),
        )
        for _, status := range combinedStatus.Statuses {
            log.Info("PR status",
                "context", status.GetContext(),
                "state", status.GetState(),
                "description", status.GetDescription(),
            )
        }
    }

    checkRuns, _, err := r.gh.Checks.ListCheckRunsForRef(ctx, resource.Owner, resource.Repo, pr.GetHead().GetSHA(), nil)
    if err != nil {
        log.Warn("failed to fetch check runs", "error", err)
    } else {
        for _, check := range checkRuns.CheckRuns {
            log.Info("PR check run",
                "name", check.GetName(),
                "status", check.GetStatus(),
                "conclusion", check.GetConclusion(),
            )
        }
    }

    // Fetch and log changed files
    files, _, err := r.gh.PullRequests.ListFiles(ctx, resource.Owner, resource.Repo, resource.Number, nil)
    if err != nil {
        log.Warn("failed to fetch changed files", "error", err)
    } else {
        for _, file := range files {
            log.Info("PR file change",
                "filename", file.GetFilename(),
                "status", file.GetStatus(),
                "additions", file.GetAdditions(),
                "deletions", file.GetDeletions(),
            )
        }
    }

    return &workqueue.ProcessResponse{}, nil
}
```

## GitHub App Setup Instructions

### Step 1: Create the GitHub App

1. Go to https://github.com/settings/apps/new (or org settings for org-owned app)

2. Fill in the basic information:
   - **GitHub App name**: `driftlessaf-pr-logger` (must be unique)
   - **Homepage URL**: `https://github.com/imjasonh/terraform-playground`
   - **Webhook URL**: Will be filled in after Terraform apply (the github-events ingress URL)
   - **Webhook secret**: Generate a random string, save it for later

3. Set permissions:
   - **Repository permissions**:
     - Pull requests: **Read-only** (to fetch PR details)
     - Contents: **Read-only** (if you want to read file contents)
     - Metadata: **Read-only** (required, auto-selected)

4. Subscribe to events:
   - Check **Pull request**

5. Where can this GitHub App be installed?
   - Select **Only on this account** for testing

6. Click **Create GitHub App**

### Step 2: Generate and Save Private Key

1. After creating the app, scroll down to **Private keys**
2. Click **Generate a private key**
3. Save the downloaded `.pem` file securely

### Step 3: Install the App

1. Go to the app's page: https://github.com/settings/apps/driftlessaf-pr-logger
2. Click **Install App** in the left sidebar
3. Select your account
4. Choose **Only select repositories** and pick `terraform-playground`
5. Click **Install**

### Step 4: Note the IDs

After installation, you'll need:
- **App ID**: Found on the app's settings page (General tab, near the top)
- **Installation ID**: Found in the URL after installing: `https://github.com/settings/installations/INSTALLATION_ID`

### Step 5: Store Secrets in GCP

```bash
# Store the private key
gcloud secrets versions add github-app-private-key \
  --project=jason-chainguard \
  --data-file=path/to/your-app.private-key.pem

# Store the webhook secret
echo -n "your-webhook-secret" | gcloud secrets versions add github-webhook-secret \
  --project=jason-chainguard \
  --data-file=-
```

### Step 6: Configure Webhook URL

After `terraform apply`, update the GitHub App's webhook URL to the `github-events` ingress URL output by Terraform.

## File Structure

```
driftlessaf/
├── main.tf              # Provider config, variables
├── networking.tf        # VPC and regional networks
├── broker.tf            # CloudEvents broker
├── github-events.tf     # GitHub webhook receiver
├── secrets.tf           # Secret Manager resources
├── reconciler.tf        # PR reconciler service
├── trigger.tf           # CloudEvent trigger for PR events
├── variables.tf         # Input variables
├── outputs.tf           # Output values (webhook URL, etc.)
└── cmd/
    └── pr-reconciler/
        ├── main.go      # Reconciler implementation
        └── go.mod       # Go module
```

## Variables Required

| Variable | Description | Example |
|----------|-------------|---------|
| `project_id` | GCP project | `jason-chainguard` |
| `github_app_id` | GitHub App ID | `123456` |
| `github_installation_id` | Installation ID | `78901234` |

## Deployment Steps

1. Create the GitHub App (follow instructions above)
2. Store secrets in Secret Manager
3. Create `terraform.tfvars`:
   ```hcl
   project_id              = "jason-chainguard"
   github_app_id           = "YOUR_APP_ID"
   github_installation_id  = "YOUR_INSTALLATION_ID"
   ```
4. Run Terraform:
   ```bash
   cd driftlessaf
   terraform init
   terraform plan
   terraform apply
   ```
5. Update GitHub App webhook URL with the output value
6. Test by opening/updating a PR in terraform-playground

## Decisions Made

- **Notification channels**: Not needed for now
- **Workqueue configuration**: Using defaults (20 concurrent workers, 100 max retries)
- **Additional PR data**: Logging labels, reviews, CI status, and file changes
- **Future actions**: To be tackled later

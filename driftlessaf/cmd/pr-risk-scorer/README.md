# PR Risk Scorer

The PR Risk Scorer is a GitHub App reconciler that automatically assesses the risk level of pull requests based on changed files and PR size.

## Features

- **Automatic Risk Assessment**: Evaluates PRs based on file patterns and size
- **Risk Labels**: Applies `risk/low`, `risk/medium`, or `risk/high` labels
- **GitHub Check Runs**: Creates check runs that fail for high-risk PRs
- **Detailed Reports**: Provides markdown reports with risk factors and affected files
- **Smart Label Management**: Removes outdated risk labels when reassessing

## Risk Levels

### High Risk (Check fails) 🚨

PRs are marked as high risk when they:
- Modify Terraform files (`*.tf`, `*.tfvars`)
- Change Docker configurations (`Dockerfile*`, `docker-compose*`)
- Update Kubernetes manifests in `k8s/`, `kubernetes/`, or `helm/` directories
- Modify CI/CD workflows (`.github/workflows/**`)
- Change dependency files (`go.mod`, `package.json`, `requirements.txt`, `Cargo.toml`)
- Include database migrations (`**/migrations/**`, `**/*migration*`)
- Have 500+ lines changed

### Medium Risk (Check passes with warning) ⚠️

PRs are marked as medium risk when they:
- Modify backend code (Go, Rust, Java, C/C++)
- Change frontend code (TypeScript, JavaScript, JSX/TSX)
- Update Python code
- Modify configuration files
- Have 200-500 lines changed

### Low Risk (Check passes) ✅

PRs are marked as low risk when they:
- Only change documentation
- Have small code changes (<200 lines in non-critical files)

## Architecture

The risk scorer follows the driftlessaf reconciler pattern:

```
GitHub PR Event → CloudEvents Broker → Workqueue → Risk Scorer
                                                         ↓
                                              Risk Assessment
                                                         ↓
                                    ┌───────────────────┴───────────────────┐
                                    ↓                                       ↓
                              Apply Labels                         Create Check Run
                          (risk/low|medium|high)              (pass/warning/fail)
```

## Implementation

### Core Components

1. **`internal/risk/risk.go`**: Risk assessment logic
   - Pattern matching for file types
   - Size-based thresholds
   - Risk level calculation
   - Label management

2. **`cmd/pr-risk-scorer/main.go`**: Reconciler service
   - gRPC workqueue server
   - GitHub API integration
   - Status tracking
   - Check run management

3. **`risk-scorer.tf`**: Infrastructure configuration
   - Service account
   - Cloud Run deployment
   - Event routing

### Risk Assessment Algorithm

```go
1. Calculate total lines changed (additions + deletions)
2. Initialize risk level as "low"
3. Check size thresholds:
   - If >= 500 lines: Set to "high"
   - Else if >= 200 lines: Set to "medium"
4. For each changed file:
   - Match against risk patterns
   - Update risk level if pattern has higher priority
5. Return assessment with risk level, reasons, and risky files
```

## Deployment

The risk scorer is deployed using Terraform:

```bash
cd driftlessaf
terraform apply
```

### Required Configuration

The following variables are required:

- `project_id`: GCP project ID
- `github_app_id`: GitHub App ID
- `github_app_key`: GitHub App private key (stored in Secret Manager)

### Environment Variables

The risk scorer uses these environment variables:

- `PORT`: HTTP port (default: 8080)
- `GITHUB_APP_ID`: GitHub App ID
- `GITHUB_PRIVATE_KEY`: GitHub App private key

## Testing

Run the test suite:

```bash
cd driftlessaf
go test ./internal/risk/...
go test ./cmd/pr-risk-scorer/...
```

All tests include:
- Pattern matching tests
- Risk level calculation tests
- Label management tests
- PR URL parsing tests
- Markdown rendering tests

## Examples

### High Risk PR Example

```
Repository: owner/repo
PR #123: "Migrate database schema"

Files Changed:
- db/migrations/001_add_users.sql
- main.tf
- k8s/deployment.yaml

Assessment:
- Risk Level: HIGH 🚨
- Files Analyzed: 3
- Lines Changed: 450
- Reasons:
  - Database migrations can affect data integrity
  - Terraform infrastructure changes can affect cost
  - Kubernetes configuration changes can affect availability
- Check Status: FAILED
```

### Medium Risk PR Example

```
Repository: owner/repo
PR #124: "Add user profile feature"

Files Changed:
- src/handlers/user.go
- src/templates/profile.tsx
- tests/user_test.go

Assessment:
- Risk Level: MEDIUM ⚠️
- Files Analyzed: 3
- Lines Changed: 320
- Reasons:
  - Backend code changes require review
  - Frontend code changes can affect UX
- Check Status: NEUTRAL
```

### Low Risk PR Example

```
Repository: owner/repo
PR #125: "Update README"

Files Changed:
- README.md
- docs/installation.md

Assessment:
- Risk Level: LOW ✅
- Files Analyzed: 2
- Lines Changed: 45
- Reasons:
  - Changes do not match high-risk patterns
- Check Status: SUCCESS
```

## Customization

You can customize the risk rules by modifying `internal/risk/risk.go`:

```go
config := risk.Config{
    Rules: []risk.Rule{
        {
            Level:       risk.LevelHigh,
            Patterns:    []string{"*.tf"},
            Description: "Terraform changes",
        },
        // Add more rules...
    },
    LargePRThreshold:  500,
    MediumPRThreshold: 200,
}
```

## Monitoring

The risk scorer provides:
- Structured logging with Cloud Logging
- Metrics via Prometheus
- Distributed tracing with OpenTelemetry
- Health checks on the gRPC endpoint

## Troubleshooting

### Check Run Not Created

If check runs aren't being created:
1. Verify GitHub App has `checks:write` permission
2. Check Cloud Logging for errors
3. Ensure the workqueue is routing events correctly

### Incorrect Risk Assessment

If risk levels seem wrong:
1. Review the risk rules in `risk.go`
2. Check pattern matching logic
3. Verify size thresholds
4. Review test cases for expected behavior

### Labels Not Applied

If labels aren't being applied:
1. Verify GitHub App has `pull_requests:write` permission
2. Check that labels exist in the repository
3. Review Cloud Logging for API errors

## Contributing

When adding new risk patterns:
1. Add the rule to `DefaultConfig()` in `risk.go`
2. Add test cases in `risk_test.go`
3. Update this README with the new pattern
4. Ensure all tests pass

## License

See the repository root for license information.

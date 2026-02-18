variable "project_id" {
  type        = string
  description = "The GCP project ID."
}

variable "github_app_id" {
  type        = string
  description = "The GitHub App ID."
}

variable "team" {
  type        = string
  description = "Team label to apply to resources."
  default     = ""
}

variable "secret_version_adder" {
  type        = string
  description = "User permitted to manage webhook secrets (e.g., user:you@company.biz)."
}

variable "deletion_protection" {
  type        = bool
  description = "Whether to enable deletion protection on resources."
  default     = false
}

variable "notification_channels" {
  type        = list(string)
  description = "List of notification channel IDs to attach to alerting policies."
}

# CI Fixer configuration
variable "enable_ci_fixer" {
  type        = bool
  description = "Whether to enable the CI fixer agent."
  default     = false
}

variable "ci_fixer_model" {
  type        = string
  description = "The Claude model to use for CI fixing."
  default     = "claude-sonnet-4@20250514"
}

variable "ci_fixer_max_turns" {
  type        = number
  description = "Maximum number of fix attempts per CI failure."
  default     = 3
}

variable "ci_fixer_label" {
  type        = string
  description = "GitHub label that triggers CI fixing."
  default     = "ci-autofix"
}

variable "ci_fixer_region" {
  type        = string
  description = "GCP region for Vertex AI Claude API calls."
  default     = "us-east5"
}

# Risk Scorer configuration
variable "risk_scorer_model" {
  type        = string
  description = "The Claude model to use for risk assessment."
  default     = "claude-sonnet-4@20250514"
}

variable "risk_scorer_region" {
  type        = string
  description = "GCP region for Vertex AI Claude API calls."
  default     = "us-east5"
}

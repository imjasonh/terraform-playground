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

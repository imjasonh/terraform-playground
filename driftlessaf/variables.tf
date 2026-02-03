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

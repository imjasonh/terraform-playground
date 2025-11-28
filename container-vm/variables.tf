variable "project_id" {
  description = "The GCP project ID."
  type        = string
}

variable "region" {
  description = "The region where the compute template is created."
  type        = string
}

variable "machine_type" {
  description = "The machine type for the VM instance template."
  type        = string
  default     = "e2-medium"
}

variable "containers" {
  description = "A map of containers to run on the VM. Each key is the container name, and the value specifies the container configuration."
  type = map(object({
    image   = string
    command = optional(string, "")
    args    = optional(list(string), [])
    env = optional(list(object({
      name  = string
      value = string
    })), [])
    restart_policy = optional(string, "always")
  }))

  validation {
    condition     = length(var.containers) > 0
    error_message = "At least one container must be specified."
  }

  validation {
    condition     = alltrue([for name, c in var.containers : can(regex("^.+@sha256:[a-f0-9]{64}$", c.image))])
    error_message = "All container images must be referenced by digest (e.g., 'gcr.io/my-project/my-app@sha256:<64 hex chars>')."
  }
}

variable "service_account_email" {
  description = "The email of the service account to use for the VM. Must be created outside this module."
  type        = string
}

# Defaulting to COS, which has Docker/containerd pre-installed.
variable "source_image_family" {
  description = "The OS image family to use. COS is recommended."
  type        = string
  default     = "cos-stable"
}

variable "source_image_project" {
  description = "The project hosting the source image."
  type        = string
  default     = "cos-cloud"
}

variable "network" {
  description = "The VPC network to attach the VM to (e.g., 'projects/PROJECT/global/networks/NETWORK' or 'NETWORK')."
  type        = string
}

variable "subnetwork" {
  description = "The subnetwork to attach the VM to (e.g., 'projects/PROJECT/regions/REGION/subnetworks/SUBNET' or 'SUBNET')."
  type        = string
}

# Observability variables
variable "enable_logging" {
  description = "Enable Cloud Logging for the VM and containers. When true, container logs are sent to Cloud Logging using the gcplogs driver."
  type        = bool
  default     = true
}

variable "enable_monitoring" {
  description = "Enable Cloud Monitoring for the VM. When true, VM metrics are collected and sent to Cloud Monitoring."
  type        = bool
  default     = true
}

variable "enable_cos_metrics" {
  description = "Enable Container-Optimized OS metrics collection for additional container and system metrics."
  type        = bool
  default     = true
}

variable "labels" {
  description = "Labels to apply to the instance template for organization and filtering in Cloud Monitoring and Logging."
  type        = map(string)
  default     = {}
}


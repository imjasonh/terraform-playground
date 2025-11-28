variable "project_id" {
  description = "The GCP project ID."
  type        = string
}

variable "region" {
  description = "The region where the compute template is created."
  type        = string
  default     = "us-central1"
}

variable "machine_type" {
  description = "The machine type for the VM instance template."
  type        = string
  default     = "e2-medium"
}

variable "container_image" {
  description = "The container image to run (e.g., 'nginx:latest' or 'gcr.io/my-project/my-app:v1')."
  type        = string
}

variable "container_name" {
  description = "The name to assign to the running Docker container."
  type        = string
  default     = "gce-container-app"
}

variable "restart_policy" {
  description = "The systemd/Docker restart policy (e.g., 'always', 'on-failure', 'no')."
  type        = string
  default     = "always"
}

variable "container_env" {
  description = "A map of environment variables to set in the container."
  type        = map(string)
  default     = {}
}

variable "container_command" {
  description = "The optional command to override the ENTRYPOINT (e.g., '/bin/bash')."
  type        = string
  default     = ""
}

variable "container_args" {
  description = "Optional arguments to pass to the container command."
  type        = list(string)
  default     = []
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


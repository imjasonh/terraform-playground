variable "group" {
  type        = string
  description = "The Chainguard group that we are subscribing to."
}

variable "dst_repo" {
  type        = string
  default     = "image-copy-ecr/lambda"
  description = "The destination repo where images should be copied to."
}

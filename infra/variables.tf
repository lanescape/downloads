variable "project_id" {
  description = "The project everything runs in, shared with other teams."
  type        = string
  default     = "lanescape-dev"
}

variable "region" {
  description = "The region of the service and its secrets: bootstrap/'s."
  type        = string
  default     = "us-central1"
}

# The image CD has just pushed, by digest, so an apply deploys exactly what
# CD built. It must be in the service's own repository.
variable "service_image" {
  description = "The service's image, by digest."
  type        = string

  validation {
    condition     = can(regex("^us-central1-docker\\.pkg\\.dev/lanescape-dev/lanescape-downloads/[a-z0-9._/-]+@sha256:[0-9a-f]{64}$", var.service_image))
    error_message = "service_image must be an image in the lanescape-downloads repository, by digest."
  }
}

# CD's run ID and attempt, on the service's template, so that every apply
# makes a new revision, even of an image already deployed: a rerun of a
# deploy whose revision couldn't start, such as the first, before its
# secrets had values, starts one afresh.
variable "deploy_id" {
  description = "CD's run ID and attempt, <run ID>-<attempt>."
  type        = string

  validation {
    condition     = can(regex("^[0-9]+-[0-9]+$", var.deploy_id))
    error_message = "deploy_id must be <run ID>-<attempt>."
  }
}

# The names of the secrets config/groups.yaml names, exactly as it writes
# them, comma-separated: what `go run ./cmd/downloads -secret-names` prints.
variable "secret_names" {
  description = "The secrets' names, comma-separated, each lanescape-downloads-*."
  type        = string

  validation {
    condition     = can(regex("^lanescape-downloads-[A-Za-z0-9_-]+(,lanescape-downloads-[A-Za-z0-9_-]+)*$", var.secret_names))
    error_message = "secret_names must be Secret Manager secrets' names, each starting lanescape-downloads-, comma-separated."
  }
}

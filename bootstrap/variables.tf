variable "project_id" {
  description = "The project everything runs in. Other teams' resources are there too, so everything here is named lanescape-downloads-*."
  type        = string
  default     = "lanescape-dev"
}

# Iowa, where Cloud Run maps a custom domain to a service. The service moves
# to Montreal, with the rest of the organization's, once Cloud Run maps
# domains there (specs/0002-deployment.md, Region).
variable "region" {
  description = "The region of the service, its images and its secrets."
  type        = string
  default     = "us-central1"
}

variable "domain" {
  description = "The service's domain. Its CNAME to ghs.googlehosted.com. is in the lanescape.dev zone already, and whoever applies this must be a verified owner of lanescape.dev."
  type        = string
  default     = "downloads.lanescape.dev"
}

variable "github_owner_id" {
  description = "The organization's ID. CD's sign-in checks it."
  type        = number
  default     = 303994381
}

variable "github_repository_id" {
  description = "This repository's ID (gh api repos/lanescape/downloads --jq .id). CD's sign-in checks it."
  type        = number
  default     = 1467557213
}

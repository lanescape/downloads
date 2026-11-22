# The service's own resources (specs/0002-deployment.md), applied only by
# Infrastructure Manager from CD, as lanescape-downloads-infra, which may
# touch nothing else of the project's. The accounts, every project-level
# grant, the images' repository and the domain mapping are in bootstrap/.

locals {
  # The service's account, which bootstrap/ makes.
  service_account = "lanescape-downloads-service@${var.project_id}.iam.gserviceaccount.com"

  # The secrets config/groups.yaml names, in order; CD passes them.
  secret_names = sort(distinct(split(",", var.secret_names)))

  labels = { lanescape-downloads = "infra" }
}

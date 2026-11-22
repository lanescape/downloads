locals {
  # Cloud Run's deterministic URL, which CD's smoke check calls.
  service_url = "https://lanescape-downloads-${data.google_project.this.number}.${var.region}.run.app"

  # Every secret infra/ makes is named so (config/groups.yaml), and the infra
  # identity's grants on secrets are limited to such names (roles.tf).
  secret_prefix = "lanescape-downloads-"

  labels = { lanescape-downloads = "bootstrap" }
}

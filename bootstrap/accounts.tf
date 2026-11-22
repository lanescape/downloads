# Every service account of the service. infra/ can make none, and holds no
# keys for any; nothing here makes a key.

resource "google_service_account" "service" {
  account_id   = "lanescape-downloads-service"
  display_name = "Lanescape Downloads service"
  description  = "The service on Cloud Run. It may read only its own secrets (infra/secrets.tf) and holds no project role."
  depends_on   = [google_project_service.apis]
}

resource "google_service_account" "deployer" {
  account_id   = "lanescape-downloads-deployer"
  display_name = "Lanescape Downloads CD"
  description  = "CD, signed in only from a push to main by cd.yml: pushes images and starts applies of infra/ (specs/0002-deployment.md)."
  depends_on   = [google_project_service.apis]
}

resource "google_service_account" "infra" {
  account_id   = "lanescape-downloads-infra"
  display_name = "Lanescape Downloads infrastructure"
  description  = "Infrastructure Manager applies infra/ as this account: the service's own resources, by name (specs/0002-deployment.md)."
  depends_on   = [google_project_service.apis]
}

# The infra identity deploys as the service; CD starts applies as the infra
# identity.
resource "google_service_account_iam_member" "act_as" {
  for_each = {
    infra_as_service  = { account = google_service_account.service.name, member = google_service_account.infra.member }
    deployer_as_infra = { account = google_service_account.infra.name, member = google_service_account.deployer.member }
  }

  service_account_id = each.value.account
  role               = "roles/iam.serviceAccountUser"
  member             = each.value.member
}

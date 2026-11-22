# The service on Cloud Run, as a shell that infra/ adopts and deploys
# (infra/run.tf), and its domain. IAM can't limit Cloud Run by name, so the
# service is made here, and the infra identity's grant is on the service
# itself: it can deploy this one, and reaches no other Cloud Run service in
# the project.
#
# The shell runs Google's sample container as the service's account, takes
# only internal traffic and keeps Cloud Run's invoker check, so nothing
# reaches it before the first deploy. After that, infra/ owns everything
# about it, and this configuration changes nothing.

resource "google_cloud_run_v2_service" "shell" {
  name                = "lanescape-downloads"
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_INTERNAL_ONLY"
  deletion_protection = true

  template {
    service_account = google_service_account.service.email

    containers {
      image = "us-docker.pkg.dev/cloudrun/container/hello"
    }
  }

  lifecycle {
    ignore_changes = all
  }

  depends_on = [google_project_service.apis]
}

resource "google_cloud_run_v2_service_iam_member" "infra" {
  name     = google_cloud_run_v2_service.shell.name
  location = google_cloud_run_v2_service.shell.location
  role     = google_project_iam_custom_role.infra_run.name
  member   = google_service_account.infra.member
}

# downloads.lanescape.dev, mapped to the service. Cloud Run makes a mapping
# only for a verified owner of the domain, which the maintainer applying
# this is and the infra identity isn't, so it's here. Its DNS record, a
# CNAME to ghs.googlehosted.com., is in the lanescape.dev zone already;
# Google provisions the certificate once it sees it.
resource "google_cloud_run_domain_mapping" "service" {
  name     = var.domain
  location = var.region

  metadata {
    namespace = var.project_id
    labels    = local.labels
  }

  spec {
    route_name = google_cloud_run_v2_service.shell.name
  }
}

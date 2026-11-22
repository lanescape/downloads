# The service on Cloud Run. Anyone may call it: it serves public downloads,
# so Cloud Run's own invoker check is off.
#
# bootstrap/ makes it as a shell, maps the domain to it, and grants the
# infra identity on it alone, since IAM can't limit Cloud Run by name; this
# adopts and deploys it. The infra identity can't make or delete a Cloud Run
# service.

import {
  to = google_cloud_run_v2_service.service
  id = "projects/lanescape-dev/locations/us-central1/services/lanescape-downloads"
}

resource "google_cloud_run_v2_service" "service" {
  name                 = "lanescape-downloads"
  description          = "Lanescape Downloads: redirects downloads.lanescape.dev/<group>/<key> to the group's provider."
  location             = var.region
  ingress              = "INGRESS_TRAFFIC_ALL"
  invoker_iam_disabled = true
  deletion_protection  = true
  labels               = local.labels

  template {
    service_account = local.service_account

    # A new revision with every apply (variables.tf).
    annotations = {
      "lanescape-downloads/deploy" = var.deploy_id
    }

    # A request is answered at once, from memory.
    timeout = "30s"

    # None while nothing's asked for, and at most two: each instance
    # answers many requests at once, since each is only a signature.
    scaling {
      min_instance_count = 0
      max_instance_count = 2
    }

    containers {
      image = var.service_image

      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "256Mi"
        }
        cpu_idle = true
      }

      env {
        name  = "DOWNLOADS_SECRETS_DIR"
        value = "/secrets"
      }

      # Each secret at /secrets/<name>/value (internal/secrets).
      dynamic "volume_mounts" {
        for_each = local.secret_names
        content {
          name       = "secret-${volume_mounts.key}"
          mount_path = "/secrets/${volume_mounts.value}"
        }
      }
    }

    dynamic "volumes" {
      for_each = local.secret_names
      content {
        name = "secret-${volumes.key}"
        secret {
          secret = google_secret_manager_secret.secrets[volumes.value].secret_id
          items {
            version = "latest"
            path    = "value"
          }
        }
      }
    }
  }

  depends_on = [google_secret_manager_secret_iam_member.service]
}

# The secrets config/groups.yaml names: only their metadata, and who may
# read them, are here. The maintainer adds each value by hand (README.md),
# and nothing here ever reads one.

resource "google_secret_manager_secret" "secrets" {
  for_each = toset(local.secret_names)

  secret_id           = each.value
  labels              = local.labels
  deletion_protection = true

  replication {
    user_managed {
      replicas {
        location = var.region
      }
    }
  }
}

resource "google_secret_manager_secret_iam_member" "service" {
  for_each = google_secret_manager_secret.secrets

  secret_id = each.value.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${local.service_account}"
}

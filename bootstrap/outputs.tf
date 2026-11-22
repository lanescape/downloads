output "service_url" {
  description = "The service's run.app URL, which CD's smoke check calls."
  value       = local.service_url
}

output "accounts" {
  description = "The service's accounts."
  value = {
    service  = google_service_account.service.email
    deployer = google_service_account.deployer.email
    infra    = google_service_account.infra.email
  }
}

output "workload_identity_provider" {
  description = "For CD's google-github-actions/auth workload_identity_provider."
  value       = google_iam_workload_identity_pool_provider.github.name
}

output "image_repository" {
  description = "Where CD pushes the images (KO_DOCKER_REPO)."
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}"
}

output "infra_manager_artifacts_bucket" {
  description = "For CD's --artifacts-gcs-bucket."
  value       = "gs://${google_storage_bucket.infra_manager.name}"
}

output "domain_records" {
  description = "The DNS records Cloud Run expects for the domain."
  value       = google_cloud_run_domain_mapping.service.status
}

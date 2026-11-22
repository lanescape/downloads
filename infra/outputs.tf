output "service_url" {
  description = "The service's run.app URL."
  value       = google_cloud_run_v2_service.service.uri
}

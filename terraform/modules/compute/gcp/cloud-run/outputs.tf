output "service_name" {
  description = "Cloud Run service name"
  value       = google_cloud_run_v2_service.main.name
}

output "service_id" {
  description = "Cloud Run service ID"
  value       = google_cloud_run_v2_service.main.id
}

output "service_uri" {
  description = "Cloud Run service URI"
  value       = google_cloud_run_v2_service.main.uri
}

output "service_url" {
  description = "Cloud Run service URL (same as URI)"
  value       = google_cloud_run_v2_service.main.uri
}

output "service_account_email" {
  description = "Service account email used by Cloud Run"
  value       = google_service_account.cloud_run.email
}

output "scheduler_job_name" {
  description = "Cloud Scheduler job name (if enabled)"
  value       = var.enable_scheduled_tasks ? google_cloud_scheduler_job.recommendations[0].name : null
}

output "latest_ready_revision" {
  description = "Latest ready revision name"
  value       = google_cloud_run_v2_service.main.latest_ready_revision
}

output "latest_created_revision" {
  description = "Latest created revision name"
  value       = google_cloud_run_v2_service.main.latest_created_revision
}

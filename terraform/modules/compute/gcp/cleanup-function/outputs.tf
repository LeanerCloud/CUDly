output "function_uri" {
  description = "URI of the Cloud Function"
  value       = google_cloudfunctions2_function.cleanup.service_config[0].uri
}

output "function_name" {
  description = "Name of the Cloud Function"
  value       = google_cloudfunctions2_function.cleanup.name
}

output "schedule" {
  description = "Cloud Scheduler schedule"
  value       = var.schedule
}

output "service_account_email" {
  description = "Service account email for the cleanup function"
  value       = google_service_account.cleanup.email
}

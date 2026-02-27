# Frontend Build and Deployment Resources for GCP
# This file handles building the frontend and uploading to Cloud Storage

# Build frontend with npm
resource "terraform_data" "frontend_build" {
  count = var.enable_frontend_build ? 1 : 0

  triggers_replace = {
    # Rebuild when package.json or source files change
    package_json = fileexists("${path.root}/${var.frontend_path}/package.json") ? filemd5("${path.root}/${var.frontend_path}/package.json") : "none"
    src_hash     = length(fileset("${path.root}/${var.frontend_path}/src", "**")) > 0 ? sha256(join("", [for f in fileset("${path.root}/${var.frontend_path}/src", "**") : filesha256("${path.root}/${var.frontend_path}/src/${f}")])) : "none"
  }

  provisioner "local-exec" {
    working_dir = "${path.root}/${var.frontend_path}"
    command     = <<-EOT
      echo "Building frontend..."
      npm install
      npm run build
      echo "✅ Frontend build complete"
    EOT
  }
}

# Upload frontend files to Cloud Storage
resource "terraform_data" "frontend_upload" {
  count = var.enable_frontend_build ? 1 : 0

  triggers_replace = {
    # Re-upload when build changes
    build_hash = terraform_data.frontend_build[0].id
    files_hash = length(fileset("${path.root}/${var.frontend_path}/dist", "**")) > 0 ? sha256(join("", [for f in fileset("${path.root}/${var.frontend_path}/dist", "**") : filemd5("${path.root}/${var.frontend_path}/dist/${f}")])) : "none"
  }

  provisioner "local-exec" {
    command = <<-EOT
      echo "Uploading frontend to Cloud Storage..."

      # Upload all static assets with long cache headers (cache-busted filenames)
      gcloud storage rsync "${path.root}/${var.frontend_path}/dist" "gs://${var.bucket_name}" \
        --recursive --delete-unmatched-destination-objects \
        --cache-control="public, max-age=31536000, immutable" \
        --project="${var.project_id}"

      # Override cache headers for HTML files (must not be cached)
      for html_file in $(gcloud storage ls "gs://${var.bucket_name}/**/*.html" "gs://${var.bucket_name}/*.html" 2>/dev/null); do
        gcloud storage objects update "$html_file" \
          --cache-control="no-cache, no-store, must-revalidate" \
          --project="${var.project_id}"
      done

      echo "Frontend uploaded to Cloud Storage"
    EOT
  }

  depends_on = [terraform_data.frontend_build[0]]
}

# Invalidate Cloud CDN cache after deployment
resource "terraform_data" "cdn_invalidation" {
  count = var.enable_frontend_build ? 1 : 0

  triggers_replace = {
    # Invalidate when files change
    upload_hash = terraform_data.frontend_upload[0].id
  }

  provisioner "local-exec" {
    command = <<-EOT
      echo "Invalidating Cloud CDN cache..."
      gcloud compute url-maps invalidate-cdn-cache ${var.project_name}-url-map \
        --path "/*" \
        --project ${var.project_id} \
        --async
      echo "✅ Cloud CDN invalidation created"
    EOT
  }

  depends_on = [terraform_data.frontend_upload[0]]
}

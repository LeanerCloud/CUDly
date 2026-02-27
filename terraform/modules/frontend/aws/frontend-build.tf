# Frontend Build and Deployment Resources
# This file handles building the frontend and uploading to S3

# Build frontend with npm
resource "terraform_data" "frontend_build" {
  count = var.enable_frontend_build ? 1 : 0

  triggers_replace = {
    # Rebuild when package.json or source files change
    package_json = fileexists("${path.root}/${var.frontend_path}/package.json") ? filemd5("${path.root}/${var.frontend_path}/package.json") : "none"
    # Hash all files in src directory (if it exists, fileset will return empty if not)
    src_hash = try(
      sha256(join("", [for f in fileset("${path.root}/${var.frontend_path}/src", "**") : filesha256("${path.root}/${var.frontend_path}/src/${f}")])),
      "none"
    )
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

# Upload all files from frontend/dist to S3 using aws s3 sync
resource "terraform_data" "frontend_sync" {
  count = var.enable_frontend_build ? 1 : 0

  triggers_replace = {
    # Re-sync when frontend build changes
    build_id = terraform_data.frontend_build[0].id
  }

  provisioner "local-exec" {
    command = <<-EOT
      echo "📤 Syncing frontend files to S3..."
      aws s3 sync "${path.root}/${var.frontend_path}/dist" "s3://${aws_s3_bucket.frontend.id}" \
        --delete \
        --cache-control "no-cache, no-store, must-revalidate" \
        --exclude "*" \
        --include "*.html" \
        --metadata-directive REPLACE

      aws s3 sync "${path.root}/${var.frontend_path}/dist" "s3://${aws_s3_bucket.frontend.id}" \
        --delete \
        --cache-control "public, max-age=31536000, immutable" \
        --exclude "*.html" \
        --metadata-directive REPLACE

      echo "✅ Frontend files synced successfully"
    EOT
  }

  depends_on = [
    terraform_data.frontend_build[0],
    aws_s3_bucket.frontend,
    aws_s3_bucket_public_access_block.frontend
  ]
}

# Invalidate CloudFront cache after deployment
resource "terraform_data" "cloudfront_invalidation" {
  count = var.enable_frontend_build ? 1 : 0

  triggers_replace = {
    # Create new invalidation when files sync changes
    sync_id = terraform_data.frontend_sync[0].id
  }

  provisioner "local-exec" {
    command = <<-EOT
      echo "🔄 Invalidating CloudFront cache..."
      aws cloudfront create-invalidation \
        --distribution-id ${aws_cloudfront_distribution.frontend.id} \
        --paths "/*" \
        --query 'Invalidation.Id' \
        --output text
      echo "✅ CloudFront invalidation created"
    EOT
  }

  depends_on = [terraform_data.frontend_sync[0]]
}

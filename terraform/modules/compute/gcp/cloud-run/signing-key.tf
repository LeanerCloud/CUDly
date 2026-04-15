# GCP Cloud KMS asymmetric signing key used by the CUDly OIDC issuer
# to sign client-assertion JWTs presented to target-cloud token
# endpoints. The private half never leaves Cloud KMS. The public half
# is published through Cloud Run's /.well-known/jwks.json endpoint.

resource "google_kms_key_ring" "signing" {
  name     = "cudly-oidc-signing"
  location = var.region
  project  = var.project_id
}

resource "google_kms_crypto_key" "signing" {
  name                       = "cudly-oidc-signing"
  key_ring                   = google_kms_key_ring.signing.id
  purpose                    = "ASYMMETRIC_SIGN"
  destroy_scheduled_duration = "86400s" # 1 day — tests redeploy often

  version_template {
    algorithm        = "RSA_SIGN_PKCS1_2048_SHA256"
    protection_level = "SOFTWARE"
  }

  lifecycle {
    prevent_destroy = false
  }
}

# The Cloud Run service account needs cloudkms.signer to call
# AsymmetricSign and cloudkms.viewer to call GetPublicKey.
resource "google_kms_crypto_key_iam_member" "cloud_run_signer" {
  crypto_key_id = google_kms_crypto_key.signing.id
  role          = "roles/cloudkms.signer"
  member        = "serviceAccount:${google_service_account.cloud_run.email}"
}

resource "google_kms_crypto_key_iam_member" "cloud_run_viewer" {
  crypto_key_id = google_kms_crypto_key.signing.id
  role          = "roles/cloudkms.viewerVersion"
  member        = "serviceAccount:${google_service_account.cloud_run.email}"
}

# The signing key version resource name CUDly passes to internal/oidc
# as CUDLY_SIGNING_KEY_RESOURCE. Cloud KMS creates version "1"
# automatically when a crypto key is first created.
locals {
  signing_key_version_resource = "${google_kms_crypto_key.signing.id}/cryptoKeyVersions/1"
}

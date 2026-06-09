# Example backend config for the CUDly GCP target federation module.
# Copy to backend.hcl (or per-environment file), fill in your bucket, then:
#
#   terraform init -backend-config=backend.hcl
#
# The bucket must exist before `terraform init` runs. Bootstrap it once:
#   gsutil mb -p <project-id> -l <region> gs://my-cudly-tf-state
#   gsutil versioning set on gs://my-cudly-tf-state
#   gsutil uniformbucketlevelaccess set on gs://my-cudly-tf-state

bucket = "my-cudly-tf-state"

# Optional: impersonate_service_account = "tf-state-rw@my-project.iam.gserviceaccount.com"

# Each invocation of this module should use a unique prefix so multiple
# target projects don't collide on the same state object.
prefix = "federation/gcp-target"

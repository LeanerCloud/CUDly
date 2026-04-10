variable "subscription_id" {
  description = "Azure subscription ID where CUDly will purchase reservations."
  type        = string
}

variable "tenant_id" {
  description = "Azure AD tenant ID."
  type        = string
}

variable "app_display_name" {
  description = "Display name for the Azure AD App Registration."
  type        = string
  default     = "CUDly"
}

variable "certificate_pem" {
  description = <<-EOT
    X.509 certificate PEM string to register with the App Registration.
    This is the PUBLIC certificate; the corresponding private key is stored in CUDly
    as azure_wif_private_key and is never managed by Terraform.

    Generate with:
      openssl genrsa -out cudly-wif.key 2048
      openssl req -new -x509 -key cudly-wif.key -out cudly-wif.crt -days 730 -subj "/CN=CUDly-WIF"
    Then paste the contents of cudly-wif.crt here and store cudly-wif.key in CUDly.
  EOT
  type        = string
  sensitive   = false
}

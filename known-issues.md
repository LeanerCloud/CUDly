# Known Issues

No critical issues at this time. The following are known limitations:

## Azure

- **Azure SMTP credential generation requires manual Azure Portal step**: Azure Communication Services SMTP credentials cannot be auto-generated via Terraform. See the documentation in `terraform/modules/secrets/azure/main.tf` for manual setup instructions.

## Azure AKS Module

- **Helm LoadBalancer IP may not be available on first apply**: The NGINX ingress LoadBalancer IP is provisioned asynchronously by Azure. The `load_balancer_ip` output uses `try()` to return `""` when pending. A subsequent `terraform apply` resolves the IP.

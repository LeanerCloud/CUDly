# Known Issues

## Azure AKS Module (not blocking Container Apps deployment)

- Kubernetes/Helm providers not configured in module - would fail on `terraform apply`
- Helm release data source race condition (LoadBalancer IP may not be ready)
- Network policy enabled but not configured
- No resource quotas or pod disruption budgets

## GCP GKE Module (not blocking Cloud Run deployment)

- No scheduled tasks implementation (Cloud Scheduler / CronJob)
- Health checks hardcoded (not configurable via variables)

## GCP Frontend (Cloud CDN + LB)

- DNS zone outputs reference resources that may not exist if subdomain zone not created

## Azure Frontend (CDN / Front Door)

- DNS zone outputs may reference non-existent resources if subdomain zone not created
- Azure SMTP credential generation requires manual Azure Portal step

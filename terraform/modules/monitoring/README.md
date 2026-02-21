# Monitoring Modules

Comprehensive monitoring infrastructure for CUDly across AWS, GCP, and Azure cloud providers.

## Overview

These Terraform modules provide complete observability solutions including:

- **Dashboards** - Visual representation of key metrics
- **Alerts** - Proactive notifications for critical issues
- **Log aggregation** - Centralized log collection and analysis
- **Distributed tracing** - Request flow across services
- **Health checks** - Service availability monitoring
- **Custom metrics** - Application-specific business metrics

---

## AWS CloudWatch Module

**Path:** `terraform/modules/monitoring/aws/`

### Features

- **SNS Topics** - Email and Slack notifications with KMS encryption
- **CloudWatch Dashboard** - Unified view of Lambda/Fargate, Database, and Application metrics
- **CloudWatch Alarms** - Automated alerts for:
  - Lambda errors, throttles, duration
  - Database CPU, connections, serverless capacity
  - Application errors (log-based metrics)
- **Log Metric Filters** - Extract metrics from application logs
- **X-Ray Tracing** - Distributed request tracing
- **CloudWatch Insights Queries** - Pre-built queries for troubleshooting

### Usage

```hcl
module "monitoring" {
  source = "../../modules/monitoring/aws"

  stack_name         = "cudly-prod"
  environment        = "prod"
  aws_region         = "us-east-1"
  compute_platform   = "lambda" # or "fargate"

  # Lambda configuration (if compute_platform is lambda)
  function_name = "cudly-prod-api"

  # Database configuration
  db_cluster_id  = module.database.cluster_id
  log_group_name = "/aws/lambda/cudly-prod-api"

  # Alert destinations
  alert_email_addresses = ["ops@example.com", "oncall@example.com"]
  slack_webhook_url     = var.slack_webhook_url

  # Optional: Customize thresholds
  lambda_error_threshold = 10
  lambda_duration_threshold = 10000 # ms
  db_cpu_threshold = 80
  db_connection_threshold = 80

  # Optional: X-Ray configuration
  enable_xray = true
  xray_sampling_rate = 0.1

  tags = {
    Environment = "prod"
    ManagedBy   = "terraform"
  }
}
```

### Outputs

```hcl
# SNS topic ARN for alerts
output "sns_topic_arn" {
  value = module.monitoring.sns_topic_arn
}

# CloudWatch dashboard name
output "dashboard_name" {
  value = module.monitoring.dashboard_name
}

# All alarm ARNs
output "alarm_arns" {
  value = {
    lambda_errors     = module.monitoring.lambda_errors_alarm_arn
    db_cpu            = module.monitoring.db_cpu_alarm_arn
    application_errors = module.monitoring.application_errors_alarm_arn
  }
}
```

### Alarms

| Alarm | Threshold | Evaluation Period | Action |
|-------|-----------|-------------------|--------|
| Lambda Errors | 10 errors | 5 minutes (2 periods) | SNS notification |
| Lambda Throttles | 10 throttles | 5 minutes (1 period) | SNS notification |
| Lambda Duration | 10 seconds avg | 5 minutes (2 periods) | SNS notification |
| DB CPU | 80% | 5 minutes (2 periods) | SNS notification |
| DB Connections | 80 connections | 5 minutes (2 periods) | SNS notification |
| DB Capacity | 1.5 ACU (75% of max) | 15 minutes (3 periods) | SNS notification |
| Application Errors | 20 errors | 5 minutes (1 period) | SNS notification |

### CloudWatch Insights Queries

**Errors by Type:**

```
fields @timestamp, @message
| filter @message like /ERROR/
| parse @message /ERROR: (?<error_type>.*?):/
| stats count() by error_type
| sort count desc
```

**Slow Requests:**

```
fields @timestamp, @message, @duration
| filter @type = "REPORT"
| filter @duration > 5000
| sort @duration desc
| limit 20
```

**Request Volume:**

```
fields @timestamp
| filter @type = "REPORT"
| stats count() as request_count by bin(5m)
```

---

## GCP Cloud Monitoring Module

**Path:** `terraform/modules/monitoring/gcp/`

### Features

- **Notification Channels** - Email and Slack notifications
- **Log Sinks** - Send error logs to Pub/Sub for processing
- **Log-based Metrics** - Extract metrics from structured logs
- **Cloud Monitoring Dashboard** - Comprehensive metrics visualization
- **Alert Policies** - Automated alerts for:
  - High error rate (5xx responses)
  - High latency (P95 response time)
  - High CPU/memory utilization
  - Database performance issues
- **Uptime Checks** - HTTP health check monitoring from multiple locations

### Usage

```hcl
module "monitoring" {
  source = "../../modules/monitoring/gcp"

  project_id     = var.project_id
  service_name   = "cudly-prod"
  environment    = "prod"
  region         = "us-central1"

  # Database configuration
  db_instance_id = module.database.instance_id

  # Service URL for uptime checks
  service_url = module.compute.service_url

  # Alert destinations
  alert_email_addresses = ["ops@example.com"]
  slack_webhook_url     = var.slack_webhook_url

  # Optional: Customize thresholds
  error_rate_threshold = 5 # requests per second
  latency_threshold = 5000 # ms
  cpu_threshold = 80 # %
  memory_threshold = 85 # %
  db_cpu_threshold = 80 # %
  db_connection_threshold = 80

  labels = {
    environment = "prod"
    managed_by  = "terraform"
  }
}
```

### Outputs

```hcl
# Notification channel IDs
output "notification_channels" {
  value = {
    email = module.monitoring.email_notification_channel_ids
    slack = module.monitoring.slack_notification_channel_id
  }
}

# Dashboard ID
output "dashboard_id" {
  value = module.monitoring.dashboard_id
}

# Alert policy IDs
output "alert_policies" {
  value = {
    high_error_rate = module.monitoring.high_error_rate_policy_id
    high_latency    = module.monitoring.high_latency_policy_id
    db_high_cpu     = module.monitoring.db_high_cpu_policy_id
  }
}
```

### Alert Policies

| Alert | Threshold | Duration | Action |
|-------|-----------|----------|--------|
| High Error Rate | 5 req/s | 5 minutes | Notification |
| High Latency | 5000ms P95 | 5 minutes | Notification |
| High CPU | 80% | 5 minutes | Notification |
| High Memory | 85% | 5 minutes | Notification |
| DB High CPU | 80% | 5 minutes | Notification |
| DB High Connections | 80 connections | 5 minutes | Notification |
| Application Errors | 10/min | 5 minutes | Notification |
| Service Unavailable | Health check fails | 3 minutes | Notification |

### Uptime Check

The module creates an uptime check that:

- Sends HTTPS GET requests to `/health` endpoint
- Checks from 3 locations: Texas, Illinois, California
- Runs every 60 seconds
- Expects HTTP 200 status
- Expects response body to contain "healthy"
- Alerts if 2 or more locations fail

---

## Azure Application Insights Module

**Path:** `terraform/modules/monitoring/azure/`

### Features

- **Application Insights** - Full-stack application monitoring
- **Log Analytics Workspace** - Centralized log aggregation
- **Action Groups** - Email and Slack notification delivery
- **Metric Alerts** - Automated alerts for:
  - High error rate
  - High response time
  - Container App CPU/memory
  - Database performance issues
- **Query Alerts** - Log-based alerts for application errors
- **Availability Tests** - Multi-region health checks
- **Workbook** - Interactive dashboard with custom visualizations

### Usage

```hcl
module "monitoring" {
  source = "../../modules/monitoring/azure"

  app_name            = "cudly-prod"
  environment         = "prod"
  location            = "eastus"
  resource_group_name = azurerm_resource_group.main.name

  # Resource IDs
  container_app_id = module.compute.container_app_id
  db_server_id     = module.database.server_id

  # Application URL for availability tests
  app_url = module.compute.app_url

  # Alert destinations
  alert_email_addresses = ["ops@example.com"]
  slack_webhook_url     = var.slack_webhook_url

  # Optional: Log retention
  log_retention_days = 30

  # Optional: Customize thresholds
  error_rate_threshold = 10
  latency_threshold = 5000 # ms
  cpu_threshold = 0.8 # cores
  memory_threshold = 400 # MB
  db_cpu_threshold = 80 # %
  db_connection_threshold = 80

  tags = {
    Environment = "prod"
    ManagedBy   = "terraform"
  }
}
```

### Outputs

```hcl
# Application Insights details
output "app_insights" {
  value = {
    id                 = module.monitoring.application_insights_id
    name               = module.monitoring.application_insights_name
    connection_string  = module.monitoring.application_insights_connection_string
  }
  sensitive = true
}

# Action group IDs
output "action_groups" {
  value = {
    email = module.monitoring.email_action_group_id
    slack = module.monitoring.slack_action_group_id
  }
}

# Alert IDs
output "alerts" {
  value = {
    high_error_rate    = module.monitoring.high_error_rate_alert_id
    high_response_time = module.monitoring.high_response_time_alert_id
    db_high_cpu        = module.monitoring.db_high_cpu_alert_id
  }
}
```

### Metric Alerts

| Alert | Threshold | Window | Frequency | Severity |
|-------|-----------|--------|-----------|----------|
| High Error Rate | 10 exceptions | 5 min | 5 min | 2 (Warning) |
| High Response Time | 5000ms avg | 5 min | 5 min | 2 (Warning) |
| High CPU | 0.8 cores | 5 min | 5 min | 2 (Warning) |
| High Memory | 400 MB | 5 min | 5 min | 2 (Warning) |
| DB High CPU | 80% | 5 min | 5 min | 2 (Warning) |
| DB High Memory | 85% | 5 min | 5 min | 2 (Warning) |
| DB High Connections | 80 connections | 5 min | 5 min | 2 (Warning) |
| Application Errors | 20 errors | 5 min | 5 min | 2 (Warning) |
| Service Unavailable | 2 location failures | 5 min | 1 min | 1 (Error) |

### Availability Test

The module creates an availability test that:

- Sends HTTPS GET requests to `/health` endpoint
- Checks from 3 Azure regions: Texas, Illinois, California
- Runs every 5 minutes
- Expects HTTP 200 status
- Expects response body to contain "healthy"
- Alerts if 2 or more locations fail

### Workbook Dashboard

The workbook includes:

- **Request Rate and Failures** - Total requests and failed requests over time
- **Response Time** - P50/P95/P99 latency trends
- **Database Performance** - CPU, memory, active connections
- **Error Trend** - Error count by severity level over time

---

## Common Configuration Patterns

### Basic Configuration (All Clouds)

**Minimal:**

```hcl
module "monitoring" {
  source = "../../modules/monitoring/{aws|gcp|azure}"

  # Cloud-specific naming
  stack_name         = "cudly-prod"  # AWS
  service_name       = "cudly-prod"  # GCP
  app_name           = "cudly-prod"  # Azure

  environment = "prod"

  # Alert destinations (required)
  alert_email_addresses = ["ops@example.com"]
}
```

**Recommended:**

```hcl
module "monitoring" {
  source = "../../modules/monitoring/{aws|gcp|azure}"

  # ... basic config ...

  # Email and Slack notifications
  alert_email_addresses = [
    "ops@example.com",
    "oncall@example.com"
  ]
  slack_webhook_url = var.slack_webhook_url

  # Customize key thresholds
  error_rate_threshold = 10
  latency_threshold = 3000 # Lower for production
  db_cpu_threshold = 70    # Lower for production

  # Tags/labels
  tags = {
    Environment = "prod"
    Team        = "platform"
    ManagedBy   = "terraform"
  }
}
```

### Multi-Environment Setup

Use Terraform workspaces or separate directories:

**terraform/environments/aws/prod/main.tf:**

```hcl
module "monitoring" {
  source = "../../../modules/monitoring/aws"

  stack_name   = "cudly-prod"
  environment  = "prod"

  alert_email_addresses = [
    "ops@example.com",
    "oncall@example.com"
  ]
  slack_webhook_url = var.slack_webhook_url_prod

  # Production thresholds (stricter)
  lambda_error_threshold = 5
  lambda_duration_threshold = 5000
  db_cpu_threshold = 70
}
```

**terraform/environments/aws/dev/main.tf:**

```hcl
module "monitoring" {
  source = "../../../modules/monitoring/aws"

  stack_name   = "cudly-dev"
  environment  = "dev"

  alert_email_addresses = ["dev-team@example.com"]

  # Development thresholds (more lenient)
  lambda_error_threshold = 20
  lambda_duration_threshold = 15000
  db_cpu_threshold = 90

  # Disable X-Ray in dev to save costs
  enable_xray = false
}
```

---

## Integrating with Application Code

### AWS CloudWatch (Go)

**Send custom metrics:**

```go
import (
    "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
    "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

func recordSavingsGenerated(ctx context.Context, amount float64) error {
    _, err := cloudwatchClient.PutMetricData(ctx, &cloudwatch.PutMetricDataInput{
        Namespace: aws.String("CUDly"),
        MetricData: []types.MetricDatum{
            {
                MetricName: aws.String("SavingsGenerated"),
                Value:      aws.Float64(amount),
                Unit:       types.StandardUnitNone,
                Timestamp:  aws.Time(time.Now()),
            },
        },
    })
    return err
}
```

**Enable X-Ray tracing:**

```go
import (
    "github.com/aws/aws-xray-sdk-go/xray"
)

func main() {
    // Wrap HTTP client
    http.DefaultClient = xray.Client(http.DefaultClient)

    // Trace segments
    ctx, seg := xray.BeginSegment(context.Background(), "fetch-recommendations")
    defer seg.Close(nil)

    // Trace subsegments
    ctx, subseg := xray.BeginSubsegment(ctx, "database-query")
    results, err := db.Query(ctx, query)
    subseg.Close(err)
}
```

### GCP Cloud Logging (Go)

**Structured logging:**

```go
import (
    "cloud.google.com/go/logging"
)

func setupLogging(ctx context.Context, projectID string) (*logging.Client, error) {
    client, err := logging.NewClient(ctx, projectID)
    if err != nil {
        return nil, err
    }

    logger := client.Logger("cudly-app")

    // Log with severity and structured fields
    logger.Log(logging.Entry{
        Severity: logging.Error,
        Payload: map[string]interface{}{
            "message":    "Failed to fetch recommendations",
            "error":      err.Error(),
            "account_id": accountID,
        },
    })

    return client, nil
}
```

### Azure Application Insights (Go)

**Initialize Application Insights:**

```go
import (
    "github.com/microsoft/ApplicationInsights-Go/appinsights"
)

func setupAppInsights(connectionString string) appinsights.TelemetryClient {
    config := appinsights.NewTelemetryConfiguration(connectionString)
    client := appinsights.NewTelemetryClientFromConfig(config)

    // Track custom events
    client.TrackEvent("PurchaseExecuted")

    // Track custom metrics
    client.TrackMetric("SavingsGenerated", 1250.00)

    // Track dependencies
    dependency := appinsights.NewRemoteDependencyTelemetry(
        "PostgreSQL",
        "Database",
        "query-recommendations",
        true,
    )
    dependency.Duration = time.Since(start)
    client.Track(dependency)

    return client
}
```

---

## Troubleshooting

### AWS CloudWatch

**Alarms not triggering:**

- Check CloudWatch Logs for Lambda/Fargate errors
- Verify SNS topic subscriptions are confirmed (check email)
- Test SNS topic: `aws sns publish --topic-arn <arn> --message "Test"`

**Missing metrics:**

- Verify log metric filter patterns match log format
- Check CloudWatch Logs Insights: Run test queries
- Ensure application is logging in expected format

**X-Ray not showing traces:**

- Verify X-Ray SDK is imported in application
- Check Lambda environment has `AWS_XRAY_TRACING_NAME` set
- Review X-Ray sampling rate (default: 10%)

### GCP Cloud Monitoring

**Alert policies not firing:**

- Check notification channel verification (email confirmation)
- Test notification: `gcloud alpha monitoring policies test <policy-name>`
- Verify metric exists: Use Metrics Explorer in console

**Log-based metrics not working:**

- Verify log filter matches log structure
- Check Logs Explorer with same filter
- Ensure logs have required JSON fields

**Uptime check failing:**

- Verify service URL is publicly accessible
- Check `/health` endpoint returns 200 with "healthy"
- Review uptime check configuration in console

### Azure Application Insights

**No telemetry data:**

- Verify Application Insights connection string is set in app
- Check instrumentation key is correct
- Review Application Insights Live Metrics Stream
- Verify app has internet access to Azure endpoints

**Alerts not sending notifications:**

- Check action group email addresses are verified
- Test action group: Use "Test" button in Azure portal
- Verify alert rule is enabled
- Check alert rule query returns data

**Availability test failing:**

- Verify app URL is publicly accessible
- Check app returns 200 status for `/health`
- Ensure response body contains "healthy"
- Review test results in Azure portal

---

## Cost Optimization

### AWS CloudWatch Costs

| Resource | Estimated Cost (per month) |
|----------|---------------------------|
| CloudWatch Logs (5 GB ingestion) | $2.50 |
| CloudWatch Logs (5 GB storage) | $0.25 |
| CloudWatch Metrics (50 custom) | $15.00 |
| CloudWatch Alarms (10 alarms) | $1.00 |
| CloudWatch Dashboard | $3.00 |
| X-Ray (1M traces, 10% sampling) | $5.00 |
| SNS (1000 notifications) | $0.50 |
| **Total** | **~$27.25** |

**Cost savings tips:**

- Use log metric filters instead of custom metrics where possible
- Reduce X-Ray sampling rate in non-production environments
- Set appropriate log retention (default: 7 days for Lambda)
- Delete unused dashboards and alarms

### GCP Cloud Monitoring Costs

| Resource | Estimated Cost (per month) |
|----------|---------------------------|
| Cloud Logging (5 GB ingestion) | $2.50 |
| Cloud Logging (5 GB storage, >30 days) | Free (first 50 GB) |
| Cloud Monitoring (50 metrics) | Free (first 150 metrics) |
| Alert Policies | Free |
| Uptime Checks (1 check) | Free (first 1M checks) |
| **Total** | **~$2.50** |

**Cost savings tips:**

- Use log sinks to export logs to cheaper storage (GCS)
- Set log retention to 30 days or less
- Use log exclusion filters to drop noisy logs
- Leverage free tier (first 50 GB logs, 150 metrics)

### Azure Application Insights Costs

| Resource | Estimated Cost (per month) |
|----------|---------------------------|
| Application Insights (5 GB ingestion) | $11.50 |
| Log Analytics (5 GB storage) | $12.50 |
| Availability Tests (1 test, 3 locations) | $3.00 |
| Action Groups | Free |
| **Total** | **~$27.00** |

**Cost savings tips:**

- Use sampling to reduce telemetry volume
- Set appropriate data retention (default: 90 days)
- Use adaptive sampling in Application Insights SDK
- Delete unused availability tests in non-production

---

## Best Practices

### Alerting Philosophy

1. **Alert on symptoms, not causes**
   - Bad: "Database CPU > 80%"
   - Good: "Error rate > threshold AND response time > threshold"

2. **Make alerts actionable**
   - Include runbook links in alert descriptions
   - Provide context (recent deployments, related metrics)
   - Use severity levels appropriately

3. **Avoid alert fatigue**
   - Set appropriate thresholds (start conservative, tune over time)
   - Use evaluation periods to filter transient issues
   - Auto-close alarms when conditions return to normal

4. **Test alerts regularly**
   - Trigger test alerts monthly
   - Verify notification delivery
   - Practice incident response procedures

### Dashboard Design

1. **Top-level metrics first**
   - Request rate, error rate, latency (RED metrics)
   - CPU, memory, disk (USE metrics)

2. **Business metrics visible**
   - Purchases executed
   - Savings generated
   - Recommendations fetched

3. **Time ranges**
   - Default: Last hour
   - Quick links: Last 5 min, last day, last week

4. **Correlate metrics**
   - Show related metrics together (CPU + memory, requests + errors)
   - Use consistent time ranges across widgets

### Log Management

1. **Use structured logging**
   - JSON format for easy parsing
   - Include request ID for tracing
   - Add context (account ID, user ID, etc.)

2. **Set appropriate retention**
   - Production: 30-90 days
   - Development: 7-14 days
   - Compliance: As required by regulations

3. **Use log levels correctly**
   - ERROR: Requires immediate attention
   - WARN: Potential issues
   - INFO: Significant business events
   - DEBUG: Detailed diagnostic information (disable in production)

---

## Next Steps

1. **Deploy monitoring modules** for your environment
2. **Configure notification channels** (verify email, test Slack)
3. **Review and tune thresholds** based on baseline metrics
4. **Set up runbooks** for common alerts
5. **Test alert delivery** monthly
6. **Review dashboards** weekly with team
7. **Analyze trends** to prevent issues before they occur

For more information, see:

- [AWS CloudWatch Documentation](https://docs.aws.amazon.com/cloudwatch/)
- [GCP Cloud Monitoring Documentation](https://cloud.google.com/monitoring/docs)
- [Azure Application Insights Documentation](https://docs.microsoft.com/en-us/azure/azure-monitor/app/app-insights-overview)

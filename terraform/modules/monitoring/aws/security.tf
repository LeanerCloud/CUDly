# Security Hub and GuardDuty resources
# Both services incur usage-based AWS charges. Enable intentionally:
#   - GuardDuty: per-GB of logs analyzed (CloudTrail, VPC Flow Logs, DNS)
#   - Security Hub: ~$0.001 per security check per resource per month
# Set enable_guardduty / enable_security_hub = true in the module call for production.

# Enable AWS Security Hub for the account
resource "aws_securityhub_account" "main" {
  count = var.enable_security_hub ? 1 : 0
}

# CIS AWS Foundations Benchmark standard
resource "aws_securityhub_standards_subscription" "cis" {
  count         = var.enable_security_hub ? 1 : 0
  depends_on    = [aws_securityhub_account.main]
  standards_arn = "arn:aws:securityhub:::ruleset/cis-aws-foundations-benchmark/v/1.2.0"
}

# AWS Foundational Security Best Practices standard
resource "aws_securityhub_standards_subscription" "afsbp" {
  count         = var.enable_security_hub ? 1 : 0
  depends_on    = [aws_securityhub_account.main]
  standards_arn = "arn:aws:securityhub:${var.aws_region}::standards/aws-foundational-security-best-practices/v/1.0.0"
}

# GuardDuty detector
resource "aws_guardduty_detector" "main" {
  count                        = var.enable_guardduty ? 1 : 0
  enable                       = true
  finding_publishing_frequency = "FIFTEEN_MINUTES"

  tags = var.tags
}

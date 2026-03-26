# Security Hub and GuardDuty resources

# Enable AWS Security Hub for the account
resource "aws_securityhub_account" "main" {}

# CIS AWS Foundations Benchmark standard
resource "aws_securityhub_standards_subscription" "cis" {
  depends_on    = [aws_securityhub_account.main]
  standards_arn = "arn:aws:securityhub:::ruleset/cis-aws-foundations-benchmark/v/1.2.0"
}

# AWS Foundational Security Best Practices standard
resource "aws_securityhub_standards_subscription" "afsbp" {
  depends_on    = [aws_securityhub_account.main]
  standards_arn = "arn:aws:securityhub:${var.aws_region}::standards/aws-foundational-security-best-practices/v/1.0.0"
}

# GuardDuty detector
resource "aws_guardduty_detector" "main" {
  enable                       = true
  finding_publishing_frequency = "FIFTEEN_MINUTES"

  tags = var.tags
}

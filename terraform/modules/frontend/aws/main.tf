# AWS CDN Module - CloudFront for custom domain + edge caching
# Origin is the compute endpoint (Lambda Function URL or Fargate ALB).
# Static files are served by the container; CloudFront caches them at the edge.

terraform {
  required_version = ">= 1.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

# CloudFront distribution with single compute origin
resource "aws_cloudfront_distribution" "frontend" {
  enabled         = true
  is_ipv6_enabled = true
  comment         = "${var.project_name} CDN Distribution"
  price_class     = var.price_class
  aliases         = length(var.domain_names) > 0 && var.acm_certificate_arn != null ? var.domain_names : []
  web_acl_id      = var.waf_acl_arn

  # Single origin: compute endpoint (Lambda Function URL or Fargate ALB)
  origin {
    domain_name = var.origin_domain_name
    origin_id   = "compute"

    custom_origin_config {
      http_port              = 80
      https_port             = 443
      origin_protocol_policy = var.origin_protocol
      origin_ssl_protocols   = ["TLSv1.2"]
    }
  }

  # Default behavior: forward all requests to compute origin
  # Static file caching is driven by Cache-Control headers from the origin
  default_cache_behavior {
    target_origin_id       = "compute"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]
    cached_methods         = ["GET", "HEAD", "OPTIONS"]
    compress               = true

    forwarded_values {
      query_string = true
      headers = [
        "Authorization",
        "X-Authorization",
        "X-API-Key",
        "X-CSRF-Token",
        "Content-Type",
        "Origin",
      ]

      cookies {
        forward = "all"
      }
    }

    # Let origin Cache-Control headers drive caching
    min_ttl     = 0
    default_ttl = 0
    max_ttl     = 31536000

    function_association {
      event_type   = "viewer-response"
      function_arn = aws_cloudfront_function.security_headers.arn
    }
  }

  # SPA routing: serve index.html for 404s from the origin
  custom_error_response {
    error_code            = 404
    response_code         = 200
    response_page_path    = "/index.html"
    error_caching_min_ttl = 10
  }

  restrictions {
    geo_restriction {
      restriction_type = var.geo_restriction_type
      locations        = var.geo_restriction_locations
    }
  }

  viewer_certificate {
    acm_certificate_arn            = var.acm_certificate_arn
    cloudfront_default_certificate = var.acm_certificate_arn == null ? true : null
    ssl_support_method             = var.acm_certificate_arn != null ? "sni-only" : null
    minimum_protocol_version       = var.acm_certificate_arn != null ? "TLSv1.2_2021" : "TLSv1"
  }

  tags = merge(var.tags, {
    Name        = "${var.project_name}-cdn"
    Environment = var.environment
  })
}

# CloudFront Function for security headers
resource "aws_cloudfront_function" "security_headers" {
  name    = "${var.project_name}-${var.environment}-security-headers"
  runtime = "cloudfront-js-1.0"
  comment = "Add security headers to responses for ${var.environment}"
  publish = true
  code    = <<-EOT
function handler(event) {
    var response = event.response;
    var headers = response.headers;

    // Security headers
    headers['strict-transport-security'] = { value: 'max-age=31536000; includeSubDomains; preload' };
    headers['x-content-type-options'] = { value: 'nosniff' };
    headers['x-frame-options'] = { value: 'DENY' };
    headers['x-xss-protection'] = { value: '1; mode=block' };
    headers['referrer-policy'] = { value: 'strict-origin-when-cross-origin' };

    // Remove server header
    delete headers['server'];

    return response;
}
EOT
}

# Route53 DNS record (if domain provided)
resource "aws_route53_record" "frontend" {
  count = var.route53_zone_id != null && length(var.domain_names) > 0 ? 1 : 0

  zone_id = var.route53_zone_id
  name    = var.domain_names[0]
  type    = "A"

  alias {
    name                   = aws_cloudfront_distribution.frontend.domain_name
    zone_id                = aws_cloudfront_distribution.frontend.hosted_zone_id
    evaluate_target_health = false
  }
}

# CloudWatch alarm for high error rates
resource "aws_cloudwatch_metric_alarm" "cloudfront_5xx" {
  alarm_name          = "${var.project_name}-cloudfront-5xx-errors"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = "2"
  metric_name         = "5xxErrorRate"
  namespace           = "AWS/CloudFront"
  period              = "300"
  statistic           = "Average"
  threshold           = "1"
  alarm_description   = "CloudFront 5xx error rate is too high"
  treat_missing_data  = "notBreaching"

  dimensions = {
    DistributionId = aws_cloudfront_distribution.frontend.id
  }

  alarm_actions = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []
}

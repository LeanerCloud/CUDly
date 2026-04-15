# KMS asymmetric signing key used by the CUDly OIDC issuer to sign
# client-assertion JWTs presented to target-cloud token endpoints
# (currently Azure AD via federated identity credentials). The private half
# never leaves KMS. The public half is published through the Lambda's
# /.well-known/jwks.json endpoint so target clouds can verify signatures
# without CUDly holding any long-lived secret.

resource "aws_kms_key" "signing" {
  description              = "CUDly ${var.stack_name} OIDC issuer signing key (RSA_2048, RS256)"
  customer_master_key_spec = "RSA_2048"
  key_usage                = "SIGN_VERIFY"
  deletion_window_in_days  = 7
  enable_key_rotation      = false # KMS asymmetric keys do not support automatic rotation

  tags = merge(var.tags, {
    Name = "${var.stack_name}-oidc-signing"
  })
}

resource "aws_kms_alias" "signing" {
  name          = "alias/${var.stack_name}-oidc-signing"
  target_key_id = aws_kms_key.signing.key_id
}

resource "aws_iam_role_policy" "signing_key_access" {
  name_prefix = "${var.stack_name}-signing-"
  role        = aws_iam_role.lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "kms:Sign",
          "kms:GetPublicKey",
          "kms:DescribeKey",
        ]
        Resource = [aws_kms_key.signing.arn]
      },
      {
        # Needed so the Lambda can look up its own Function URL at
        # cold start and prime the OIDC issuer cache. This closes the
        # race where a scheduled task triggered before any inbound
        # HTTP request would find oidc.IssuerURL() empty and fail to
        # mint Azure AD client_assertion JWTs. Scoped to this function
        # only via wildcard on the function-name prefix.
        Effect = "Allow"
        Action = [
          "lambda:GetFunctionUrlConfig",
        ]
        Resource = "arn:aws:lambda:*:*:function:${var.stack_name}-api*"
      }
    ]
  })
}

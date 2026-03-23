data "aws_caller_identity" "current" {}

resource "aws_iam_role" "cudly_deploy" {
  name = "cudly-terraform-deploy"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:user/cristi" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = {
    Project   = "CUDly"
    ManagedBy = "terraform"
  }
}

resource "aws_iam_role_policy_attachment" "networking" {
  role       = aws_iam_role.cudly_deploy.name
  policy_arn = aws_iam_policy.networking.arn
}

resource "aws_iam_role_policy_attachment" "compute" {
  role       = aws_iam_role.cudly_deploy.name
  policy_arn = aws_iam_policy.compute.arn
}

resource "aws_iam_role_policy_attachment" "data" {
  role       = aws_iam_role.cudly_deploy.name
  policy_arn = aws_iam_policy.data.arn
}

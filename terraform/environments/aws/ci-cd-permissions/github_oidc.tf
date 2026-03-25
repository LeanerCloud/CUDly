resource "aws_iam_openid_connect_provider" "github" {
  count = var.github_repo != "" ? 1 : 0

  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]

  # SHA1 thumbprint of GitHub's OIDC TLS certificate.
  # AWS now validates GitHub tokens against the JWKS endpoint directly, so this
  # is effectively a formality, but at least one thumbprint is required by the API.
  thumbprint_list = ["6938fd4d98bab03faadb97b34396831e3780aea1"]
}

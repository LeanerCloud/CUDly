module github.com/LeanerCloud/CUDly/pkg

go 1.23

toolchain go1.24.4

// This module contains shared types, provider interfaces, and the exchange package.
// The exchange package has AWS SDK dependencies for RI exchange operations.

require (
	github.com/aws/aws-sdk-go-v2 v1.40.1
	github.com/aws/aws-sdk-go-v2/config v1.26.2
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.251.2
	github.com/aws/aws-sdk-go-v2/service/sts v1.26.6
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/aws/aws-sdk-go-v2/credentials v1.16.13 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.14.10 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.15 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.15 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.7.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.18.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.21.5 // indirect
	github.com/aws/smithy-go v1.24.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

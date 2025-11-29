module github.com/LeanerCloud/CUDly/providers/aws

go 1.22

toolchain go1.24.4

require (
	github.com/LeanerCloud/CUDly/pkg v0.0.0
	github.com/aws/aws-sdk-go-v2 v1.39.2
	github.com/aws/aws-sdk-go-v2/config v1.26.2
	github.com/aws/aws-sdk-go-v2/service/costexplorer v1.51.2
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.251.2
	github.com/aws/aws-sdk-go-v2/service/elasticache v1.50.3
	github.com/aws/aws-sdk-go-v2/service/memorydb v1.31.4
	github.com/aws/aws-sdk-go-v2/service/opensearch v1.52.3
	github.com/aws/aws-sdk-go-v2/service/organizations v1.45.3
	github.com/aws/aws-sdk-go-v2/service/rds v1.97.3
	github.com/aws/aws-sdk-go-v2/service/redshift v1.58.3
	github.com/aws/aws-sdk-go-v2/service/savingsplans v1.24.2
	github.com/aws/aws-sdk-go-v2/service/sts v1.26.6
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/aws/aws-sdk-go-v2/credentials v1.16.13 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.14.10 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.9 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.9 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.7.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.18.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.21.5 // indirect
	github.com/aws/smithy-go v1.23.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/LeanerCloud/CUDly/pkg => ../../pkg

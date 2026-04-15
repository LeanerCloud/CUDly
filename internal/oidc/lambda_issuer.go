package oidc

import (
	"context"
	"fmt"
	"os"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

// LambdaFunctionURLClient is the subset of the AWS Lambda API the
// issuer-cache primer needs. Exposed as an interface so tests can
// inject a fake without dialling AWS.
type LambdaFunctionURLClient interface {
	GetFunctionUrlConfig(ctx context.Context, params *lambda.GetFunctionUrlConfigInput, optFns ...func(*lambda.Options)) (*lambda.GetFunctionUrlConfigOutput, error)
}

// PrimeIssuerURLFromLambda looks up the running Lambda's own Function
// URL via lambda:GetFunctionUrlConfig and stores
// <function-url-without-trailing-slash> + OIDCBasePath in the package
// cache. This closes the cold-start race where a scheduled task (or
// any code path that doesn't go through the HTTP handler first)
// triggers a credential exchange before the cache has been populated
// from an inbound request.
//
// No-op (and returns nil) when not running in Lambda
// (AWS_LAMBDA_FUNCTION_NAME is empty). Errors are returned to the
// caller so startup code can log them, but they should not fail cold
// start — the request-driven population path is still a backstop.
func PrimeIssuerURLFromLambda(ctx context.Context) error {
	fn := os.Getenv("AWS_LAMBDA_FUNCTION_NAME")
	if fn == "" {
		return nil
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("oidc: load aws config for function url lookup: %w", err)
	}
	return primeIssuerURLFromLambdaClient(ctx, lambda.NewFromConfig(cfg), fn)
}

// primeIssuerURLFromLambdaClient is the test-seam variant of
// PrimeIssuerURLFromLambda that accepts an injected client.
func primeIssuerURLFromLambdaClient(ctx context.Context, client LambdaFunctionURLClient, functionName string) error {
	out, err := client.GetFunctionUrlConfig(ctx, &lambda.GetFunctionUrlConfigInput{
		FunctionName: &functionName,
	})
	if err != nil {
		return fmt.Errorf("oidc: lambda GetFunctionUrlConfig: %w", err)
	}
	if out == nil || out.FunctionUrl == nil || *out.FunctionUrl == "" {
		return fmt.Errorf("oidc: lambda GetFunctionUrlConfig returned empty url")
	}
	base := strings.TrimRight(*out.FunctionUrl, "/")
	SetIssuerURL(base + "/oidc")
	return nil
}

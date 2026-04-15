package oidc

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

type fakeLambdaClient struct {
	url string
	err error
}

func (f *fakeLambdaClient) GetFunctionUrlConfig(_ context.Context, _ *lambda.GetFunctionUrlConfigInput, _ ...func(*lambda.Options)) (*lambda.GetFunctionUrlConfigOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &lambda.GetFunctionUrlConfigOutput{FunctionUrl: &f.url}, nil
}

func TestPrimeIssuerURLFromLambda(t *testing.T) {
	// Reset the package-level cache between tests by overwriting.
	SetIssuerURL("https://reset-marker/oidc")
	// Trailing slash in the returned URL must be stripped before
	// appending /oidc — otherwise we'd cache a double-slash issuer.
	client := &fakeLambdaClient{url: "https://fn.lambda-url.us-east-1.on.aws/"}
	if err := primeIssuerURLFromLambdaClient(context.Background(), client, "cudly-dev-api"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := "https://fn.lambda-url.us-east-1.on.aws/oidc"
	if got := IssuerURL(); got != want {
		t.Errorf("issuer cache = %q, want %q", got, want)
	}
}

func TestPrimeIssuerURLFromLambdaError(t *testing.T) {
	client := &fakeLambdaClient{err: errors.New("access denied")}
	err := primeIssuerURLFromLambdaClient(context.Background(), client, "cudly-dev-api")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPrimeIssuerURLFromLambdaEmptyURL(t *testing.T) {
	client := &fakeLambdaClient{url: ""}
	err := primeIssuerURLFromLambdaClient(context.Background(), client, "cudly-dev-api")
	if err == nil {
		t.Fatal("expected error on empty url")
	}
}

package oidc

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

type fakeLambdaClient struct {
	err error
	url string
}

func (f *fakeLambdaClient) GetFunctionUrlConfig(_ context.Context, _ *lambda.GetFunctionUrlConfigInput, _ ...func(*lambda.Options)) (*lambda.GetFunctionUrlConfigOutput, error) { //nolint:revive // must match SDK method name: (*lambda.Client).GetFunctionUrlConfig
	if f.err != nil {
		return nil, f.err
	}
	return &lambda.GetFunctionUrlConfigOutput{FunctionUrl: &f.url}, nil
}

func TestPrimeIssuerURLFromLambda(t *testing.T) {
	// Reset the package-level cache between tests by overwriting.
	if err := SetIssuerURL("https://reset-marker/oidc"); err != nil {
		t.Fatalf("reset SetIssuerURL: %v", err)
	}
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

// ---------------------------------------------------------------------------
// 03-L4 — SetIssuerURL must reject non-https and relative URLs
// ---------------------------------------------------------------------------

func TestSetIssuerURL_RequiresAbsoluteHTTPS(t *testing.T) {
	cases := []struct {
		url     string
		wantErr bool
	}{
		{"https://cudly.example.com/oidc", false},
		{"https://fn.lambda-url.us-east-1.on.aws/oidc", false},
		{"", false}, // empty = no-op, not an error
		{"http://cudly.example.com/oidc", true},
		{"/oidc", true},
		{"ftp://cudly.example.com/oidc", true},
		{"//cudly.example.com/oidc", true},
		{"not-a-url", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.url, func(t *testing.T) {
			err := SetIssuerURL(tc.url)
			if tc.wantErr && err == nil {
				t.Errorf("SetIssuerURL(%q) expected error, got nil", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("SetIssuerURL(%q) unexpected error: %v", tc.url, err)
			}
		})
	}
}

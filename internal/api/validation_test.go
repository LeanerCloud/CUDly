package api

import (
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
)

func TestValidateRegion(t *testing.T) {
	tests := []struct {
		name      string
		region    string
		wantError bool
	}{
		{"empty region is valid", "", false},
		{"valid AWS region", "us-east-1", false},
		{"valid AWS region with numbers", "eu-west-2", false},
		{"valid GCP region", "us-central1", false},
		{"valid Azure region", "eastus", false},
		{"region with only letters", "useast", false},
		{"invalid region with uppercase", "US-EAST-1", true},
		{"invalid region with underscore", "us_east_1", true},
		{"invalid region with special chars", "us-east-1!", true},
		{"invalid region with spaces", "us east 1", true},
		{"region too long", strings.Repeat("a", 65), true},
		{"region at max length", strings.Repeat("a", 64), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRegion(tt.region)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateProvider(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		wantError bool
	}{
		{"empty provider is valid", "", false},
		{"aws is valid", "aws", false},
		{"azure is valid", "azure", false},
		{"gcp is valid", "gcp", false},
		{"all is valid", "all", false},
		{"invalid provider", "invalid", true},
		{"uppercase AWS is invalid", "AWS", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProvider(tt.provider)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateServiceName(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		wantError   bool
	}{
		{"empty service is valid", "", false},
		{"valid service name", "rds", false},
		{"valid service with hyphen", "elastic-cache", false},
		{"valid service with numbers", "ec2", false},
		{"valid mixed case", "RDS", false},
		{"invalid with underscore", "elastic_cache", true},
		{"invalid with special chars", "rds!", true},
		{"invalid with spaces", "rds aurora", true},
		{"service too long", strings.Repeat("a", 65), true},
		{"service at max length", strings.Repeat("a", 64), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServiceName(tt.serviceName)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateServicePath(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantError bool
	}{
		{"valid path", "aws/rds", false},
		{"valid path with hyphen", "aws/elastic-cache", false},
		{"valid path with underscore", "aws/rds_aurora", false},
		{"path traversal attack", "aws/../etc/passwd", true},
		{"double slash", "aws//rds", true},
		{"no slash", "awsrds", true},
		{"too many slashes", "aws/rds/aurora", true},
		{"special characters", "aws/rds!", true},
		{"leading slash", "/aws/rds", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServicePath(tt.path)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateUUID(t *testing.T) {
	tests := []struct {
		name      string
		uuid      string
		wantError bool
	}{
		{"valid UUID", "12345678-1234-1234-1234-123456789abc", false},
		{"valid UUID uppercase", "12345678-1234-1234-1234-123456789ABC", false},
		{"valid UUID mixed case", "12345678-1234-1234-1234-123456789AbC", false},
		{"invalid - no hyphens", "123456781234123412341234567890ab", true},
		{"invalid - wrong length", "12345678-1234-1234-1234-12345678", true},
		{"invalid - extra chars", "12345678-1234-1234-1234-123456789abcd", true},
		{"invalid - non-hex", "12345678-1234-1234-1234-123456789xyz", true},
		{"empty string", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUUID(tt.uuid)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateContentType(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		body      string
		headers   map[string]string
		wantError bool
	}{
		{"GET request without body", "GET", "", nil, false},
		{"POST with json content type", "POST", `{"key": "value"}`, map[string]string{"Content-Type": "application/json"}, false},
		{"POST with json and charset", "POST", `{"key": "value"}`, map[string]string{"Content-Type": "application/json; charset=utf-8"}, false},
		{"PUT with json content type", "PUT", `{"key": "value"}`, map[string]string{"content-type": "application/json"}, false},
		{"POST with form content type", "POST", "key=value", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, false},
		{"POST without body is ok", "POST", "", nil, false},
		{"POST with body but no content type", "POST", `{"key": "value"}`, nil, true},
		{"POST with unsupported content type", "POST", `{"key": "value"}`, map[string]string{"Content-Type": "text/plain"}, true},
		{"DELETE without body", "DELETE", "", nil, false},
		{"PATCH with json", "PATCH", `{"key": "value"}`, map[string]string{"Content-Type": "application/json"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &events.LambdaFunctionURLRequest{
				Body:    tt.body,
				Headers: tt.headers,
				RequestContext: events.LambdaFunctionURLRequestContext{
					HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
						Method: tt.method,
					},
				},
			}
			err := validateContentType(req)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateRequestBodySize(t *testing.T) {
	tests := []struct {
		name      string
		bodySize  int
		wantError bool
	}{
		{"empty body", 0, false},
		{"small body", 100, false},
		{"body at limit", MaxRequestBodySize, false},
		{"body over limit", MaxRequestBodySize + 1, true},
		{"large body over limit", MaxRequestBodySize * 2, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.Repeat("a", tt.bodySize)
			err := validateRequestBodySize(body)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

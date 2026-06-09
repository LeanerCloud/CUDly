package api

import (
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
)

func TestAccountHasCredentialFreePath(t *testing.T) {
	cases := []struct {
		name string
		acct *config.CloudAccount
		want bool
	}{
		{
			name: "aws role_arn — federated",
			acct: &config.CloudAccount{Provider: "aws", AWSAuthMode: "role_arn"},
			want: true,
		},
		{
			name: "aws workload_identity_federation",
			acct: &config.CloudAccount{Provider: "aws", AWSAuthMode: "workload_identity_federation"},
			want: true,
		},
		{
			name: "aws access_keys — stored",
			acct: &config.CloudAccount{Provider: "aws", AWSAuthMode: "access_keys"},
			want: false,
		},
		{
			name: "azure managed_identity — ambient",
			acct: &config.CloudAccount{Provider: "azure", AzureAuthMode: "managed_identity"},
			want: true,
		},
		{
			name: "azure workload_identity_federation",
			acct: &config.CloudAccount{Provider: "azure", AzureAuthMode: "workload_identity_federation"},
			want: true,
		},
		{
			name: "azure client_secret — stored",
			acct: &config.CloudAccount{Provider: "azure", AzureAuthMode: "client_secret"},
			want: false,
		},
		{
			name: "gcp application_default — ambient",
			acct: &config.CloudAccount{Provider: "gcp", GCPAuthMode: "application_default"},
			want: true,
		},
		{
			name: "gcp WIF with audience — federated",
			acct: &config.CloudAccount{
				Provider:       "gcp",
				GCPAuthMode:    "workload_identity_federation",
				GCPWIFAudience: "//iam.googleapis.com/projects/1/locations/global/workloadIdentityPools/x/providers/y",
			},
			want: true,
		},
		{
			name: "gcp WIF without audience — legacy stored JSON path",
			acct: &config.CloudAccount{Provider: "gcp", GCPAuthMode: "workload_identity_federation"},
			want: false,
		},
		{
			name: "gcp service_account_key — stored",
			acct: &config.CloudAccount{Provider: "gcp", GCPAuthMode: "service_account_key"},
			want: false,
		},
		{
			name: "unknown provider",
			acct: &config.CloudAccount{Provider: "oci"},
			want: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := accountHasCredentialFreePath(c.acct); got != c.want {
				t.Errorf("accountHasCredentialFreePath(%+v) = %v, want %v", c.acct, got, c.want)
			}
		})
	}
}

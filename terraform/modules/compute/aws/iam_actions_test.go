// Package aws_test guards the AWS runtime IAM action lists in the lambda and
// fargate Terraform modules against drift from the actions the application
// code actually calls.
//
// Regression test for issue #1149 (INF-01): both runtime modules granted the
// legacy Elasticsearch-era reserved-instance actions
// (es:DescribeReservedElasticsearchInstances, ...) while the application uses
// the OpenSearch SDK (opensearch.NewFromConfig), whose operations
// DescribeReservedInstances / DescribeReservedInstanceOfferings /
// PurchaseReservedInstanceOffering authorize against the distinct new-style
// es:* action names. The mismatch made every host-account OpenSearch RI call
// return AccessDenied on Terraform deployments.
package aws_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// runtimeModuleFiles are the Terraform runtime modules that define the IAM
// policy for the deployed application identity.
var runtimeModuleFiles = []string{
	filepath.Join("lambda", "main.tf"),
	filepath.Join("fargate", "main.tf"),
}

// requiredOpenSearchActions are the IAM actions required by the OpenSearch
// API operations invoked in providers/aws/services/opensearch/client.go.
var requiredOpenSearchActions = []string{
	"es:DescribeReservedInstances",
	"es:DescribeReservedInstanceOfferings",
	"es:PurchaseReservedInstanceOffering",
}

// legacyElasticsearchActionPattern matches the pre-OpenSearch action names
// (e.g. es:DescribeReservedElasticsearchInstances) that no code path uses.
var legacyElasticsearchActionPattern = regexp.MustCompile(`es:\w*ReservedElasticsearch\w*`)

func TestRuntimeModulesGrantOpenSearchActions(t *testing.T) {
	for _, rel := range runtimeModuleFiles {
		t.Run(rel, func(t *testing.T) {
			data, err := os.ReadFile(rel)
			if err != nil {
				t.Fatalf("reading %s: %v", rel, err)
			}
			content := string(data)

			if legacy := legacyElasticsearchActionPattern.FindAllString(content, -1); len(legacy) > 0 {
				t.Errorf("%s grants legacy Elasticsearch-era actions %v; the code calls the OpenSearch API, which authorizes against the new-style es:* action names", rel, legacy)
			}

			for _, action := range requiredOpenSearchActions {
				if !strings.Contains(content, `"`+action+`"`) {
					t.Errorf("%s is missing required OpenSearch action %q (called by providers/aws/services/opensearch/client.go)", rel, action)
				}
			}
		})
	}
}

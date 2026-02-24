package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/LeanerCloud/CUDly/ci_cd_sanity_tests/pkg/sanity/azure"
)

func main() {
	var (
		subID          = flag.String("subscription-id", "", "Azure subscription ID (or set AZURE_SUBSCRIPTION_ID)")
		expectedTenant = flag.String("expected-tenant", "", "Expected Azure tenant ID (optional)")
		expectedSub    = flag.String("expected-subscription", "", "Expected Azure subscription ID (optional)")
		outPath        = flag.String("out", "azure_sanity_report.json", "Output JSON report path")
		timeoutSec     = flag.Int("timeout-sec", 120, "Timeout seconds")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
	defer cancel()

	rep, err := azure.Run(ctx, azure.Options{
		SubscriptionID:   *subID,
		ExpectedTenantID: *expectedTenant,
		ExpectedSubID:    *expectedSub,
		Timeout:          time.Duration(*timeoutSec) * time.Second,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "azure sanity run failed: %v\n", err)
		os.Exit(2)
	}

	if err := rep.WriteJSON(*outPath); err != nil {
		fmt.Fprintf(os.Stderr, "write report failed: %v\n", err)
		os.Exit(2)
	}

	if rep.HasFailures() {
		fmt.Fprintf(os.Stderr, "azure sanity: FAIL (see %s)\n", *outPath)
		os.Exit(1)
	}

	fmt.Printf("azure sanity: PASS (see %s)\n", *outPath)
}

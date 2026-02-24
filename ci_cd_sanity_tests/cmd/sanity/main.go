package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/LeanerCloud/CUDly/ci_cd_sanity_tests/pkg/sanity/aws"
)

func main() {
	var (
		region          = flag.String("region", "us-east-1", "AWS region for sanity checks")
		expectedAccount = flag.String("expected-account", "", "Expected AWS Account ID (optional)")
		maxList         = flag.Int("max-list", 5, "Max instances to list for EC2 sample (default 5). RDS uses 20..100.")
		outPath         = flag.String("out", "sanity_report.json", "Output JSON report path")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	rep, err := aws.Run(ctx, aws.Options{
		Region:          *region,
		ExpectedAccount: *expectedAccount,
		MaxList:         int32(*maxList),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sanity run failed: %v\n", err)
		os.Exit(2)
	}

	if err := rep.WriteJSON(*outPath); err != nil {
		fmt.Fprintf(os.Stderr, "write report failed: %v\n", err)
		os.Exit(2)
	}

	if rep.HasFailures() {
		fmt.Fprintf(os.Stderr, "sanity checks: FAIL (see %s)\n", *outPath)
		os.Exit(1)
	}

	fmt.Printf("sanity checks: PASS (see %s)\n", *outPath)
}

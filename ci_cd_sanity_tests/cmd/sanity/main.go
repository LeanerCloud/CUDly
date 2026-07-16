package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/LeanerCloud/CUDly/ci_cd_sanity_tests/pkg/sanity/aws"
)

// requireInt32Range exits with an error when n is outside [1, math.MaxInt32].
func requireInt32Range(flagName string, n int) {
	if n < 1 || n > (1<<31-1) {
		fmt.Fprintf(os.Stderr, "ERROR: %s must be between 1 and math.MaxInt32\n", flagName)
		os.Exit(2)
	}
}

func main() {
	var (
		region          = flag.String("region", "us-east-1", "AWS region for sanity checks")
		expectedAccount = flag.String("expected-account", "", "Expected AWS Account ID (optional)")
		maxList         = flag.Int("max-list", 5, "Max instances to list for EC2 sample (default 5). RDS uses 20..100.")
		outPath         = flag.String("out", "sanity_report.json", "Output JSON report path")
	)
	flag.Parse()
	requireInt32Range("--max-list", *maxList)

	if *maxList < 1 || *maxList > math.MaxInt32 {
		fmt.Fprintf(os.Stderr, "ERROR: --max-list must be between 1 and %d, got %d\n", math.MaxInt32, *maxList)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)

	rep, err := aws.Run(ctx, aws.Options{
		Region:          *region,
		ExpectedAccount: *expectedAccount,
		MaxList:         int32(*maxList), // #nosec G115 -- range-validated above (1 <= maxList <= math.MaxInt32); int->int32 cannot overflow
	})
	if err != nil {
		cancel()
		fmt.Fprintf(os.Stderr, "sanity run failed: %v\n", err)
		os.Exit(2)
	}

	if err := rep.WriteJSON(*outPath); err != nil {
		cancel()
		fmt.Fprintf(os.Stderr, "write report failed: %v\n", err)
		os.Exit(2)
	}

	if rep.HasFailures() {
		cancel()
		fmt.Fprintf(os.Stderr, "sanity checks: FAIL (see %s)\n", *outPath)
		os.Exit(1)
	}

	cancel()
	fmt.Printf("sanity checks: PASS (see %s)\n", *outPath)
}

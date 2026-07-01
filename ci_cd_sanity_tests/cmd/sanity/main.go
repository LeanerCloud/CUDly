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

func main() {
	os.Exit(run())
}

func run() int {
	var (
		region          = flag.String("region", "us-east-1", "AWS region for sanity checks")
		expectedAccount = flag.String("expected-account", "", "Expected AWS Account ID (optional)")
		maxList         = flag.Int("max-list", 5, "Max instances to list for EC2 sample (default 5). RDS uses 20..100.")
		outPath         = flag.String("out", "sanity_report.json", "Output JSON report path")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	maxListVal := *maxList
	if maxListVal < 0 || maxListVal > math.MaxInt32 {
		fmt.Fprintf(os.Stderr, "-max-list value %d out of range [0, %d]\n", maxListVal, math.MaxInt32)
		return 2
	}
	rep, err := aws.Run(ctx, aws.Options{
		Region:          *region,
		ExpectedAccount: *expectedAccount,
		MaxList:         int32(maxListVal),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sanity run failed: %v\n", err)
		return 2
	}

	if err := rep.WriteJSON(*outPath); err != nil {
		fmt.Fprintf(os.Stderr, "write report failed: %v\n", err)
		return 2
	}

	if rep.HasFailures() {
		fmt.Fprintf(os.Stderr, "sanity checks: FAIL (see %s)\n", *outPath)
		return 1
	}

	fmt.Printf("sanity checks: PASS (see %s)\n", *outPath)
	return 0
}

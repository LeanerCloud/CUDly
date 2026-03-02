package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	commitaws "github.com/LeanerCloud/CUDly/ci_cd_sanity_tests/pkg/commitments/aws"
)

type Output struct {
	Mode       string `json:"mode"` // dry-run | execute
	Region     string `json:"region"`
	AccountChk string `json:"expected_account,omitempty"`

	ReservedIDs      []string `json:"reserved_instance_ids"`
	TargetOfferingID string   `json:"target_offering_id"`
	TargetCount      int32    `json:"target_count"`
	MaxPaymentDueUSD string   `json:"max_payment_due_usd,omitempty"`
	ExchangeID       string   `json:"exchange_id,omitempty"`
	Quote            any      `json:"quote"`
	Error            string   `json:"error,omitempty"`
}

func parseIDs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func main() {
	var (
		region          = flag.String("region", "us-east-1", "AWS region")
		expectedAccount = flag.String("expected-account", "", "Safety check: expected AWS account ID (optional)")

		riIDsCSV       = flag.String("ri-ids", "", "Comma-separated Convertible Reserved Instance IDs to exchange (required)")
		targetOffering = flag.String("target-offering-id", "", "Target RI offering ID (required)")
		targetCount    = flag.Int("target-count", 1, "Target instance count (default 1)")

		// Execution gating
		execute       = flag.Bool("execute", false, "Actually execute the exchange (default false = quote only)")
		ack           = flag.String("ack", "", "Must be 'YES' to execute (safety)")
		maxPaymentDue = flag.String("max-payment-due-usd", "", "Max allowed paymentDue from quote (required for execute). Example: 5.00")

		outPath    = flag.String("out", "ri_exchange_result.json", "Output JSON path")
		timeoutSec = flag.Int("timeout-sec", 180, "Timeout seconds")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
	defer cancel()

	ids := parseIDs(*riIDsCSV)
	if len(ids) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: --ri-ids is required (comma-separated)")
		os.Exit(2)
	}
	if strings.TrimSpace(*targetOffering) == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --target-offering-id is required")
		os.Exit(2)
	}

	o := Output{
		Region:           *region,
		AccountChk:       *expectedAccount,
		ReservedIDs:      ids,
		TargetOfferingID: *targetOffering,
		TargetCount:      int32(*targetCount),
	}

	if !*execute {
		o.Mode = "dry-run"
		q, err := commitaws.GetExchangeQuote(ctx, commitaws.ExchangeQuoteRequest{
			Region:           *region,
			ExpectedAccount:  *expectedAccount,
			ReservedIDs:      ids,
			TargetOfferingID: *targetOffering,
			TargetCount:      int32(*targetCount),
			DryRun:           false, // false = real quote; true would only check IAM permissions
		})
		if err != nil {
			o.Error = err.Error()
			o.Quote = q
			write(o, *outPath)
			fmt.Fprintf(os.Stderr, "quote: FAIL (see %s)\n", *outPath)
			os.Exit(1)
		}
		o.Quote = q
		write(o, *outPath)

		if !q.IsValidExchange {
			fmt.Fprintf(os.Stderr, "quote: INVALID (%s) (see %s)\n", q.ValidationFailureReason, *outPath)
			os.Exit(1)
		}
		fmt.Printf("quote: OK (valid=%v, paymentDue=%s %s) (see %s)\n", q.IsValidExchange, q.PaymentDueRaw, q.CurrencyCode, *outPath)
		os.Exit(0)
	}

	// Execute path
	o.Mode = "execute"
	if strings.TrimSpace(*ack) != "YES" {
		o.Error = "refusing to execute: pass --ack YES"
		write(o, *outPath)
		fmt.Fprintf(os.Stderr, "execute: REFUSED (see %s)\n", *outPath)
		os.Exit(2)
	}
	if strings.TrimSpace(*maxPaymentDue) == "" {
		o.Error = "refusing to execute: --max-payment-due-usd is required as a safety cap"
		write(o, *outPath)
		fmt.Fprintf(os.Stderr, "execute: REFUSED (see %s)\n", *outPath)
		os.Exit(2)
	}
	maxRat, err := commitaws.ParseDecimalRat(*maxPaymentDue)
	if err != nil {
		o.Error = err.Error()
		write(o, *outPath)
		fmt.Fprintf(os.Stderr, "execute: BAD INPUT (see %s)\n", *outPath)
		os.Exit(2)
	}
	o.MaxPaymentDueUSD = maxRat.FloatString(2)

	exID, q, err := commitaws.ExecuteExchange(ctx, commitaws.ExchangeExecuteRequest{
		Region:           *region,
		ExpectedAccount:  *expectedAccount,
		ReservedIDs:      ids,
		TargetOfferingID: *targetOffering,
		TargetCount:      int32(*targetCount),
		MaxPaymentDueUSD: maxRat,
	})
	o.Quote = q
	if err != nil {
		o.Error = err.Error()
		write(o, *outPath)
		fmt.Fprintf(os.Stderr, "execute: FAIL (see %s)\n", *outPath)
		os.Exit(1)
	}

	o.ExchangeID = exID
	write(o, *outPath)
	fmt.Printf("execute: OK exchangeId=%s (see %s)\n", exID, *outPath)
}

func write(v any, path string) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal json for %s: %v\n", path, err)
		return
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write %s: %v\n", path, err)
	}
}

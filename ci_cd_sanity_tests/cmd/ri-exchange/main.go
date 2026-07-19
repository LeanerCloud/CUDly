package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/exchange"
)

// Output is the JSON-serialized result written to disk after a quote or
// exchange execution; it captures inputs, the AWS quote, and any error so the
// CI step can archive the artifact and surface a human-readable summary.
type Output struct {
	Quote            any      `json:"quote"`
	Mode             string   `json:"mode"`
	Region           string   `json:"region"`
	AccountChk       string   `json:"expected_account,omitempty"`
	TargetOfferingID string   `json:"target_offering_id"`
	MaxPaymentDueUSD string   `json:"max_payment_due_usd,omitempty"`
	ExchangeID       string   `json:"exchange_id,omitempty"`
	Error            string   `json:"error,omitempty"`
	ReservedIDs      []string `json:"reserved_instance_ids"`
	TargetCount      int32    `json:"target_count"`
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

// validateRequiredFlags checks that the required CLI flags are present and
// that --target-count can safely be narrowed to int32. It exits on the first
// failure so callers do not need to handle the error return.
func validateRequiredFlags(riIDsCSV, targetOffering string, ids []string, targetCount int) {
	if len(ids) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: --ri-ids is required (comma-separated)")
		os.Exit(2)
	}
	if strings.TrimSpace(targetOffering) == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --target-offering-id is required")
		os.Exit(2)
	}
	if targetCount < 1 || targetCount > math.MaxInt32 {
		fmt.Fprintf(os.Stderr, "ERROR: --target-count must be between 1 and %d, got %d\n", math.MaxInt32, targetCount)
		os.Exit(2)
	}
	_ = riIDsCSV // used indirectly via ids
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

	ids := parseIDs(*riIDsCSV)
	validateRequiredFlags(*riIDsCSV, *targetOffering, ids, *targetCount)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)

	o := Output{
		Region:           *region,
		AccountChk:       *expectedAccount,
		ReservedIDs:      ids,
		TargetOfferingID: *targetOffering,
		TargetCount:      int32(*targetCount), // #nosec G115 -- range-validated above (1 <= targetCount <= math.MaxInt32); int->int32 cannot overflow
	}

	if !*execute {
		o.Mode = "dry-run"
		q, err := exchange.GetExchangeQuote(ctx, exchange.ExchangeQuoteRequest{
			Region:           *region,
			ExpectedAccount:  *expectedAccount,
			ReservedIDs:      ids,
			TargetOfferingID: *targetOffering,
			TargetCount:      int32(*targetCount), // #nosec G115 -- range-validated above (1 <= targetCount <= math.MaxInt32); int->int32 cannot overflow
			DryRun:           false,               // IAMCheckOnly: false = real quote, true = only verify IAM permissions
		})
		if err != nil {
			o.Error = err.Error()
			o.Quote = q
			writeOrExit(o, *outPath)
			cancel()
			fmt.Fprintf(os.Stderr, "quote: FAIL (see %s)\n", *outPath)
			os.Exit(1)
		}
		o.Quote = q
		writeOrExit(o, *outPath)

		if !q.IsValidExchange {
			cancel()
			fmt.Fprintf(os.Stderr, "quote: INVALID (%s) (see %s)\n", q.ValidationFailureReason, *outPath)
			os.Exit(1)
		}
		cancel()
		fmt.Printf("quote: OK (valid=%v, paymentDue=%s %s) (see %s)\n", q.IsValidExchange, q.PaymentDueRaw, q.CurrencyCode, *outPath)
		os.Exit(0)
	}

	// Execute path
	o.Mode = "execute"
	if strings.TrimSpace(*ack) != "YES" {
		o.Error = "refusing to execute: pass --ack YES"
		writeOrExit(o, *outPath)
		cancel()
		fmt.Fprintf(os.Stderr, "execute: REFUSED (see %s)\n", *outPath)
		os.Exit(2)
	}
	if strings.TrimSpace(*maxPaymentDue) == "" {
		o.Error = "refusing to execute: --max-payment-due-usd is required as a safety cap"
		writeOrExit(o, *outPath)
		cancel()
		fmt.Fprintf(os.Stderr, "execute: REFUSED (see %s)\n", *outPath)
		os.Exit(2)
	}
	maxRat, err := exchange.ParseDecimalRat(*maxPaymentDue)
	if err != nil {
		o.Error = err.Error()
		writeOrExit(o, *outPath)
		cancel()
		fmt.Fprintf(os.Stderr, "execute: BAD INPUT (see %s)\n", *outPath)
		os.Exit(2)
	}
	o.MaxPaymentDueUSD = maxRat.FloatString(2)

	exID, q, err := exchange.ExecuteExchange(ctx, exchange.ExchangeExecuteRequest{
		Region:           *region,
		ExpectedAccount:  *expectedAccount,
		ReservedIDs:      ids,
		TargetOfferingID: *targetOffering,
		TargetCount:      int32(*targetCount), // #nosec G115 -- range-validated above (1 <= targetCount <= math.MaxInt32); int->int32 cannot overflow
		MaxPaymentDueUSD: maxRat,
	})
	o.Quote = q
	if err != nil {
		o.Error = err.Error()
		writeOrExit(o, *outPath)
		cancel()
		fmt.Fprintf(os.Stderr, "execute: FAIL (see %s)\n", *outPath)
		os.Exit(1)
	}

	o.ExchangeID = exID
	writeOrExit(o, *outPath)
	cancel()
	fmt.Printf("execute: OK exchangeId=%s (see %s)\n", exID, *outPath)
}

func write(v any, path string) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal json for %s: %v\n", path, err)
		return err
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write %s: %v\n", path, err)
		return err
	}
	return nil
}

// writeOrExit writes output to path and exits with code 1 if writing fails.
func writeOrExit(v any, path string) {
	if err := write(v, path); err != nil {
		os.Exit(1)
	}
}

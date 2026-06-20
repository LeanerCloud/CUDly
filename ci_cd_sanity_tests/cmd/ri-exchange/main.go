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

func main() {
	os.Exit(run())
}

// runArgs holds the parsed CLI arguments for run().
type runArgs struct {
	region          string
	expectedAccount string
	targetOffering  string
	ack             string
	maxPaymentDue   string
	outPath         string
	ids             []string
	timeoutSec      int
	targetCount32   int32
	execute         bool
}

// parseArgs parses CLI flags and validates required inputs.
// Returns (args, exit code) where exit code 0 means success.
func parseArgs() (args runArgs, code int) {
	region := flag.String("region", "us-east-1", "AWS region")
	expectedAccount := flag.String("expected-account", "", "Safety check: expected AWS account ID (optional)")
	riIDsCSV := flag.String("ri-ids", "", "Comma-separated Convertible Reserved Instance IDs to exchange (required)")
	targetOffering := flag.String("target-offering-id", "", "Target RI offering ID (required)")
	targetCount := flag.Int("target-count", 1, "Target instance count (default 1)")
	execute := flag.Bool("execute", false, "Actually execute the exchange (default false = quote only)")
	ack := flag.String("ack", "", "Must be 'YES' to execute (safety)")
	maxPaymentDue := flag.String("max-payment-due-usd", "", "Max allowed paymentDue from quote (required for execute). Example: 5.00")
	outPath := flag.String("out", "ri_exchange_result.json", "Output JSON path")
	timeoutSec := flag.Int("timeout-sec", 180, "Timeout seconds")
	flag.Parse()

	ids := parseIDs(*riIDsCSV)
	if len(ids) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: --ri-ids is required (comma-separated)")
		return runArgs{}, 2
	}
	if strings.TrimSpace(*targetOffering) == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --target-offering-id is required")
		return runArgs{}, 2
	}
	tcVal := *targetCount
	if tcVal < 0 || tcVal > math.MaxInt32 {
		fmt.Fprintf(os.Stderr, "ERROR: --target-count value %d out of range [0, %d]\n", tcVal, math.MaxInt32)
		return runArgs{}, 2
	}
	return runArgs{
		region:          *region,
		expectedAccount: *expectedAccount,
		ids:             ids,
		targetOffering:  *targetOffering,
		targetCount32:   int32(tcVal),
		execute:         *execute,
		ack:             *ack,
		maxPaymentDue:   *maxPaymentDue,
		outPath:         *outPath,
		timeoutSec:      *timeoutSec,
	}, 0
}

// runQuote handles the dry-run (quote-only) path.
func runQuote(ctx context.Context, a *runArgs, o *Output) int {
	o.Mode = "dry-run"
	q, err := exchange.GetExchangeQuote(ctx, exchange.ExchangeQuoteRequest{
		Region:           a.region,
		ExpectedAccount:  a.expectedAccount,
		ReservedIDs:      a.ids,
		TargetOfferingID: a.targetOffering,
		TargetCount:      a.targetCount32,
		DryRun:           false, // IAMCheckOnly: false = real quote, true = only verify IAM permissions
	})
	if err != nil {
		o.Error = err.Error()
		o.Quote = q
		writeOrExit(*o, a.outPath)
		fmt.Fprintf(os.Stderr, "quote: FAIL (see %s)\n", a.outPath)
		return 1
	}
	o.Quote = q
	writeOrExit(*o, a.outPath)
	if !q.IsValidExchange {
		fmt.Fprintf(os.Stderr, "quote: INVALID (%s) (see %s)\n", q.ValidationFailureReason, a.outPath)
		return 1
	}
	fmt.Printf("quote: OK (valid=%v, paymentDue=%s %s) (see %s)\n", q.IsValidExchange, q.PaymentDueRaw, q.CurrencyCode, a.outPath)
	return 0
}

func run() int {
	a, code := parseArgs()
	if code != 0 {
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(a.timeoutSec)*time.Second)
	defer cancel()

	o := Output{
		Region:           a.region,
		AccountChk:       a.expectedAccount,
		ReservedIDs:      a.ids,
		TargetOfferingID: a.targetOffering,
		TargetCount:      a.targetCount32,
	}

	if !a.execute {
		return runQuote(ctx, &a, &o)
	}

	// Execute path
	o.Mode = "execute"
	if strings.TrimSpace(a.ack) != "YES" {
		o.Error = "refusing to execute: pass --ack YES"
		writeOrExit(o, a.outPath)
		fmt.Fprintf(os.Stderr, "execute: REFUSED (see %s)\n", a.outPath)
		return 2
	}
	if strings.TrimSpace(a.maxPaymentDue) == "" {
		o.Error = "refusing to execute: --max-payment-due-usd is required as a safety cap"
		writeOrExit(o, a.outPath)
		fmt.Fprintf(os.Stderr, "execute: REFUSED (see %s)\n", a.outPath)
		return 2
	}
	maxRat, err := exchange.ParseDecimalRat(a.maxPaymentDue)
	if err != nil {
		o.Error = err.Error()
		writeOrExit(o, a.outPath)
		fmt.Fprintf(os.Stderr, "execute: BAD INPUT (see %s)\n", a.outPath)
		return 2
	}
	o.MaxPaymentDueUSD = maxRat.FloatString(2)

	exID, q, err := exchange.ExecuteExchange(ctx, exchange.ExchangeExecuteRequest{
		Region:           a.region,
		ExpectedAccount:  a.expectedAccount,
		ReservedIDs:      a.ids,
		TargetOfferingID: a.targetOffering,
		TargetCount:      a.targetCount32,
		MaxPaymentDueUSD: maxRat,
	})
	o.Quote = q
	if err != nil {
		o.Error = err.Error()
		writeOrExit(o, a.outPath)
		fmt.Fprintf(os.Stderr, "execute: FAIL (see %s)\n", a.outPath)
		return 1
	}

	o.ExchangeID = exID
	writeOrExit(o, a.outPath)
	fmt.Printf("execute: OK exchangeId=%s (see %s)\n", exID, a.outPath)
	return 0
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

// Package reporter renders scored recommendation results as human-readable text tables.
package reporter

import (
	"bytes"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/LeanerCloud/CUDly/pkg/scorer"
)

const (
	tabMinWidth = 0
	tabPadding  = 2
)

// RenderTable returns a formatted table of recommendations that passed the scorer.
// Columns: Cloud, Account, Region, Service, Type, Term, Count, Est. Cost, Est. Savings, Savings%, Break-even, Commitment.
func RenderTable(result scorer.ScoredResult) string {
	if len(result.Passed) == 0 {
		return "No recommendations passed the filters.\n"
	}
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, tabMinWidth, 0, tabPadding, ' ', 0)

	fmt.Fprintln(w, "Cloud\tAccount\tRegion\tService\tType\tTerm\tCount\tEst.Cost\tEst.Savings\tSavings%\tBreak-even\tCommitment")
	fmt.Fprintln(w, "-----\t-------\t------\t-------\t----\t----\t-----\t--------\t-----------\t---------\t----------\t----------")

	for _rvc := range result.Passed {
		rec := result.Passed[_rvc]
		breakEven := "-"
		if rec.BreakEvenMonths > 0 {
			breakEven = fmt.Sprintf("%.1f mo", rec.BreakEvenMonths)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t$%.2f\t$%.2f\t%.1f%%\t%s\t%s\n",
			rec.Provider,
			rec.AccountName,
			rec.Region,
			rec.Service,
			rec.ResourceType,
			rec.Term,
			rec.Count,
			rec.CommitmentCost,
			rec.EstimatedSavings,
			rec.SavingsPercentage,
			breakEven,
			rec.CommitmentType,
		)
	}
	w.Flush() // #nosec G104 -- writing to bytes.Buffer which never returns an error
	return buf.String()
}

// RenderExcluded returns a formatted table of recommendations that were filtered out.
// Columns: Cloud, Account, Region, Service, Type, Term, Savings%, FilterReason.
func RenderExcluded(result scorer.ScoredResult) string {
	if len(result.Filtered) == 0 {
		return ""
	}
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, tabMinWidth, 0, tabPadding, ' ', 0)

	fmt.Fprintln(w, "Cloud\tAccount\tRegion\tService\tType\tTerm\tSavings%\tFilterReason")
	fmt.Fprintln(w, "-----\t-------\t------\t-------\t----\t----\t---------\t------------")

	for _rvc := range result.Filtered {
		fr := result.Filtered[_rvc]
		rec := fr.Recommendation
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%.1f%%\t%s\n",
			rec.Provider,
			rec.AccountName,
			rec.Region,
			rec.Service,
			rec.ResourceType,
			rec.Term,
			rec.SavingsPercentage,
			fr.FilterReason,
		)
	}
	w.Flush() // #nosec G104 -- writing to bytes.Buffer which never returns an error
	return buf.String()
}

// RenderSummary returns a one-paragraph summary: total estimated savings and cost for
// passed recommendations, plus count of filtered recommendations with a reason breakdown.
//
// EstimatedSavings is monthly: sourced from AWS's EstimatedMonthlySavingsAmount
// (see providers/aws/recommendations/parser_ri.go). CommitmentCost is the
// upfront portion (one-time), so the two figures are NOT on the same timescale.
func RenderSummary(result scorer.ScoredResult) string {
	var totalSavings, totalCost float64
	for _rvc := range result.Passed {
		rec := result.Passed[_rvc]
		totalSavings += rec.EstimatedSavings
		totalCost += rec.CommitmentCost
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Passed: %d recommendations — estimated savings $%.2f/mo, upfront commitment $%.2f\n",
		len(result.Passed), totalSavings, totalCost)

	if len(result.Filtered) > 0 {
		// Group filtered reasons
		reasons := make(map[string]int)
		for _rvc := range result.Filtered {
			fr := result.Filtered[_rvc]
			// Use the first word as the reason category
			key := firstWord(fr.FilterReason)
			reasons[key]++
		}
		fmt.Fprintf(&sb, "Filtered: %d recommendations", len(result.Filtered))
		first := true
		for reason, count := range reasons {
			if first {
				fmt.Fprintf(&sb, " (%s: %d", reason, count)
				first = false
			} else {
				fmt.Fprintf(&sb, ", %s: %d", reason, count)
			}
		}
		if !first {
			fmt.Fprint(&sb, ")")
		}
		fmt.Fprintln(&sb)
	}

	return sb.String()
}

// firstWord returns the first whitespace-delimited word in s, or s itself.
func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i]
	}
	return s
}

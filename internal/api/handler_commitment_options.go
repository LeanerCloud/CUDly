// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"errors"

	"github.com/LeanerCloud/CUDly/internal/commitmentopts"
	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// commitmentOptionsResponse is the JSON shape returned by
// GET /api/commitment-options. On success, AWS carries the probed combos
// keyed by service (rds, elasticache, ...). Azure and GCP are deliberately
// omitted — those commitment rules stay hardcoded in the frontend because
// their APIs don't expose a comparable probe.
type commitmentOptionsResponse struct {
	Status string                             `json:"status"`
	AWS    map[string][]commitmentOptionCombo `json:"aws,omitempty"`
}

// commitmentOptionCombo is one (term, payment) tuple as the frontend
// consumes it. Dropping Provider/Service from the persisted Combo shape
// keeps the wire payload compact.
type commitmentOptionCombo struct {
	Term    int    `json:"term"`
	Payment string `json:"payment"`
}

// getCommitmentOptions returns the dynamically-probed AWS commitment
// options. On any error or when the probe hasn't run yet, it returns
// {"status":"unavailable"} (200, not a 5xx) so the frontend can fall
// back to its hardcoded defaults without tripping the generic
// error-toast path.
func (h *Handler) getCommitmentOptions(ctx context.Context) (*commitmentOptionsResponse, error) {
	if h.commitmentOpts == nil {
		return &commitmentOptionsResponse{Status: "unavailable"}, nil
	}
	opts, err := h.commitmentOpts.Get(ctx)
	if err != nil {
		// Any error collapses to "unavailable" so a transient DB blip
		// or context cancellation doesn't break the Settings page
		// overlay fetch. ErrNoData is the quiet path (no account
		// connected / fresh install); anything else is logged so
		// operators can still trace DB/connection issues.
		if !errors.Is(err, commitmentopts.ErrNoData) {
			logging.Warnf("commitmentopts handler: %v", err)
		}
		return &commitmentOptionsResponse{Status: "unavailable"}, nil
	}

	awsOpts := opts["aws"]
	out := make(map[string][]commitmentOptionCombo, len(awsOpts))
	for svc, combos := range awsOpts {
		out[svc] = make([]commitmentOptionCombo, 0, len(combos))
		for _, c := range combos {
			out[svc] = append(out[svc], commitmentOptionCombo{Term: c.TermYears, Payment: c.Payment})
		}
	}
	if len(out) == 0 {
		// Probe ran but returned nothing for AWS (e.g. every probe got
		// filtered out by normalization). Treat as unavailable so the
		// frontend falls through to its hardcoded rules rather than
		// rendering an empty constraint set.
		return &commitmentOptionsResponse{Status: "unavailable"}, nil
	}
	return &commitmentOptionsResponse{Status: "ok", AWS: out}, nil
}

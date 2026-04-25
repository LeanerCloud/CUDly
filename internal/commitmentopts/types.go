// Package commitmentopts discovers which (term, payment) commitment tuples
// each AWS commitment-capable service actually sells, persists the result,
// and exposes it to the API layer so the frontend can hide impossible
// combinations instead of hardcoding them.
//
// The package never blocks a save on missing data: if no probe has been
// persisted yet (or the probe failed), Service.Validate falls back to
// permissive-true and the frontend's hardcoded rules still gate the UI.
package commitmentopts

import (
	"context"
	"errors"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
)

// Combo is one (provider, service, term, payment) tuple harvested from the
// reserved-offerings APIs. TermYears is 1 or 3; Payment is one of
// "all-upfront", "partial-upfront", "no-upfront". Other durations and legacy
// utilization-style payment options are dropped during normalization.
type Combo struct {
	Provider  string
	Service   string
	TermYears int
	Payment   string
}

// Options groups valid combos by provider and service. Shape:
//
//	Options["aws"]["rds"] = []Combo{...}
//
// Only AWS is ever populated today — Azure and GCP stay hardcoded in the
// frontend because their commitment APIs don't have an equivalent probe.
type Options map[string]map[string][]Combo

// Prober probes a single AWS service's reserved-offerings API and returns
// every unique (term, payment) tuple it exposes. Implementations live in
// probe.go, one per service.
type Prober interface {
	// Service returns the canonical service name used as the Options key
	// and persisted in commitment_options_combos.service.
	Service() string

	// Probe issues paginated Describe*Offerings calls against a canonical
	// small instance type, normalizes the results, and returns unique
	// Combos. Errors bubble up — the orchestrating Service treats ANY
	// probe failure as "don't persist" (all-or-nothing).
	Probe(ctx context.Context, cfg aws.Config) ([]Combo, error)
}

// Store persists probe results. Implementations must treat the combos table
// as idempotent (ON CONFLICT DO NOTHING) so concurrent writers don't trip
// each other.
type Store interface {
	// Get returns the cached Options and a boolean indicating whether a
	// probe run row exists. The boolean is the authoritative "cache warm"
	// signal — an empty combos slice plus a present run row means "we
	// probed, AWS returned nothing matching our normalizer", and callers
	// should NOT re-probe.
	Get(ctx context.Context) (Options, bool, error)

	// Save writes the probe run row and all combos atomically. If a run
	// row already exists (concurrent writer won the race), Save is a
	// no-op rather than an error.
	Save(ctx context.Context, combos []Combo, sourceAccountID string) error

	// HasData reports whether a probe run row exists. Used as a cheap
	// pre-check before acquiring the prober lock.
	HasData(ctx context.Context) (bool, error)
}

// AccountLister is the narrow subset of config.StoreInterface that Service
// needs. Defining it here (rather than depending on the full interface)
// keeps tests small and avoids a circular import when the API layer wires
// up the real store.
type AccountLister interface {
	ListCloudAccounts(ctx context.Context, filter config.CloudAccountFilter) ([]config.CloudAccount, error)
}

// ErrNoData signals "we don't have cached commitment options and can't
// produce them right now" (no AWS account connected, or a probe failed).
// Callers MUST NOT treat this as a hard error — the API handler maps it to
// `{"status":"unavailable"}` and Validate maps it to permissive-true.
var ErrNoData = errors.New("commitment options unavailable")

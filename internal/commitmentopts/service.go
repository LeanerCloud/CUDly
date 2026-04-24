package commitmentopts

import (
	"context"
	"fmt"
	"sync"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-sdk-go-v2/aws"
	"golang.org/x/sync/errgroup"
)

// BuildConfigFn resolves an aws.Config for a given CloudAccount. The real
// wiring uses credentials.ResolveAWSCredentialProvider; tests substitute a
// stub that returns a zero aws.Config.
type BuildConfigFn func(ctx context.Context, account *config.CloudAccount) (aws.Config, error)

// Service orchestrates probe-and-cache of AWS commitment options.
//
// Concurrency model:
//   - Get is DB-first. The DB lookup is lock-free.
//   - On a cache miss, Get acquires mu, re-checks the DB under the lock
//     (to absorb a concurrent writer), then probes. The lock is per-
//     process; the singleton PK on commitment_options_probe_runs handles
//     cross-process races.
//   - Probes run concurrently under errgroup. Any probe error aborts the
//     whole attempt and NOTHING is persisted — we never want a partial
//     snapshot to set the "cache warm" sentinel.
type Service struct {
	store       Store
	accounts    AccountLister
	buildConfig BuildConfigFn
	probers     []Prober
	mu          sync.Mutex
}

// New returns a Service wired to the given dependencies. Pass
// DefaultProbers() for the real probe set; tests inject stubs.
func New(store Store, accounts AccountLister, buildConfig BuildConfigFn, probers []Prober) *Service {
	return &Service{
		store:       store,
		accounts:    accounts,
		buildConfig: buildConfig,
		probers:     probers,
	}
}

// Get returns cached Options if they exist, otherwise probes. On any
// failure (no AWS account connected, probe error) it returns ErrNoData
// without persisting; callers map that to the unavailable status.
func (s *Service) Get(ctx context.Context) (Options, error) {
	// Fast path: probe run already persisted.
	if opts, ok, err := s.store.Get(ctx); err != nil {
		return nil, fmt.Errorf("read commitmentopts store: %w", err)
	} else if ok {
		return opts, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Re-check under the lock — a peer goroutine may have just persisted.
	if opts, ok, err := s.store.Get(ctx); err != nil {
		return nil, fmt.Errorf("read commitmentopts store: %w", err)
	} else if ok {
		return opts, nil
	}

	return s.probeAndPersist(ctx)
}

// probeAndPersist runs every configured Prober against the first enabled
// AWS account. Returns ErrNoData (WITHOUT persisting) if any step fails.
func (s *Service) probeAndPersist(ctx context.Context) (Options, error) {
	account, err := s.findAWSAccount(ctx)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, ErrNoData
	}

	cfg, err := s.buildConfig(ctx, account)
	if err != nil {
		logging.Warnf("commitmentopts: probe aborted — build config account=%s: %v", account.ID, err)
		return nil, ErrNoData
	}

	// Fan out: one probe per service, first error wins.
	results := make([][]Combo, len(s.probers))
	group, gctx := errgroup.WithContext(ctx)
	for i, p := range s.probers {
		i, p := i, p
		group.Go(func() error {
			combos, err := p.Probe(gctx, cfg)
			if err != nil {
				return fmt.Errorf("probe %s: %w", p.Service(), err)
			}
			results[i] = combos
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		// All-or-nothing: a single service failing must NOT produce a
		// half-populated cache that subsequent Get calls would trust.
		// Log the first error so an operator can diagnose missing IAM
		// permissions or SDK regressions without tailing per-service logs.
		logging.Warnf("commitmentopts: probe aborted — account=%s: %v", account.ID, err)
		return nil, ErrNoData
	}

	var all []Combo
	for _, r := range results {
		all = append(all, r...)
	}

	if err := s.store.Save(ctx, all, account.ExternalID); err != nil {
		return nil, fmt.Errorf("persist commitment options: %w", err)
	}

	return buildOptions(all), nil
}

// findAWSAccount returns the first enabled AWS account, or nil if none
// exist. Nil + nil error is the "no AWS connected" signal.
func (s *Service) findAWSAccount(ctx context.Context) (*config.CloudAccount, error) {
	provider := "aws"
	accounts, err := s.accounts.ListCloudAccounts(ctx, config.CloudAccountFilter{Provider: &provider})
	if err != nil {
		return nil, fmt.Errorf("list cloud accounts: %w", err)
	}
	for i := range accounts {
		if accounts[i].Provider == "aws" {
			return &accounts[i], nil
		}
	}
	return nil, nil
}

// Validate reports whether (provider, service, term, payment) is a legal
// combination according to the cached probe data.
//
// Fallback behaviour: if no probe data exists (the server has never
// successfully probed) Validate returns true so saves aren't blocked when
// we can't verify. The frontend's hardcoded rules are the user-facing
// gate; this check is belt-and-braces.
func (s *Service) Validate(ctx context.Context, provider, service string, term int, payment string) (bool, error) {
	opts, err := s.Get(ctx)
	if err != nil {
		if err == ErrNoData {
			return true, nil
		}
		return false, err
	}

	byService, ok := opts[provider]
	if !ok {
		// No combos for this provider at all — nothing to validate
		// against. Permissive fallback keeps parity with ErrNoData.
		return true, nil
	}
	combos, ok := byService[service]
	if !ok {
		// We have data for this provider but not this service. The
		// service is not commitment-capable in our probe set (e.g.
		// Savings Plans) — permissive fallback.
		return true, nil
	}
	for _, c := range combos {
		if c.TermYears == term && c.Payment == payment {
			return true, nil
		}
	}
	return false, nil
}

// buildOptions groups a flat Combo slice into the nested Options map the
// API handler returns.
func buildOptions(combos []Combo) Options {
	if len(combos) == 0 {
		return Options{}
	}
	out := make(Options)
	for _, c := range combos {
		byService := out[c.Provider]
		if byService == nil {
			byService = make(map[string][]Combo)
			out[c.Provider] = byService
		}
		byService[c.Service] = append(byService[c.Service], c)
	}
	return out
}

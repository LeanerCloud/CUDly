package commitmentopts

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStore is a memory-backed Store used throughout service_test.go. It
// tracks call counts so tests can assert the Save-once invariant.
type fakeStore struct {
	mu       sync.Mutex
	opts     Options
	has      bool
	saves    int32
	saveErr  error
	getErr   error
	hasErr   error
	savedID  string
	savedCnt int
	saveHook func([]Combo, string)
}

func (f *fakeStore) Get(ctx context.Context) (Options, bool, error) {
	if f.getErr != nil {
		return nil, false, f.getErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.has {
		return nil, false, nil
	}
	return f.opts, true, nil
}

func (f *fakeStore) Save(ctx context.Context, combos []Combo, sourceAccountID string) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	atomic.AddInt32(&f.saves, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opts = buildOptions(combos)
	f.has = true
	f.savedID = sourceAccountID
	f.savedCnt = len(combos)
	if f.saveHook != nil {
		f.saveHook(combos, sourceAccountID)
	}
	return nil
}

func (f *fakeStore) HasData(ctx context.Context) (bool, error) {
	if f.hasErr != nil {
		return false, f.hasErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.has, nil
}

// fakeAccounts returns a fixed list.
type fakeAccounts struct {
	accounts []config.CloudAccount
	err      error
}

func (f *fakeAccounts) ListCloudAccounts(ctx context.Context, filter config.CloudAccountFilter) ([]config.CloudAccount, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.accounts, nil
}

// stubProber returns a fixed set of combos (or an error).
type stubProber struct {
	name   string
	combos []Combo
	err    error
}

func (s *stubProber) Service() string { return s.name }
func (s *stubProber) Probe(ctx context.Context, _ aws.Config) ([]Combo, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.combos, nil
}

func noopBuildConfig(ctx context.Context, _ *config.CloudAccount) (aws.Config, error) {
	return aws.Config{}, nil
}

func awsAccount(id string) config.CloudAccount {
	return config.CloudAccount{ID: id, Provider: "aws", ExternalID: id, Enabled: true}
}

// ---------------------------------------------------------------------------

func TestService_Get_DBHitShortCircuits(t *testing.T) {
	// Seed the store as if a prior probe already landed. The service must
	// never invoke the probers.
	cached := Options{"aws": {"rds": {{Provider: "aws", Service: "rds", TermYears: 1, Payment: "all-upfront"}}}}
	store := &fakeStore{opts: cached, has: true}

	probed := false
	probers := []Prober{&stubProber{name: "rds", combos: []Combo{{Provider: "aws", Service: "rds", TermYears: 3, Payment: "all-upfront"}}}}
	// Wrap to detect any call.
	wrapped := []Prober{proberFunc{name: "rds", fn: func() ([]Combo, error) {
		probed = true
		return probers[0].(*stubProber).combos, nil
	}}}

	accounts := &fakeAccounts{accounts: []config.CloudAccount{awsAccount("acct")}}
	svc := New(store, accounts, noopBuildConfig, wrapped)

	got, err := svc.Get(context.Background())
	require.NoError(t, err)
	assert.Equal(t, cached, got)
	assert.False(t, probed, "probers must not run when the DB is already warm")
	assert.Zero(t, atomic.LoadInt32(&store.saves))
}

func TestService_Get_NoAWSAccountReturnsErrNoData(t *testing.T) {
	store := &fakeStore{}
	accounts := &fakeAccounts{accounts: nil}
	svc := New(store, accounts, noopBuildConfig, []Prober{&stubProber{name: "rds"}})

	_, err := svc.Get(context.Background())
	assert.ErrorIs(t, err, ErrNoData)
	assert.Zero(t, atomic.LoadInt32(&store.saves))
}

func TestService_Get_AllProbesOKPersistsAndReturns(t *testing.T) {
	store := &fakeStore{}
	accounts := &fakeAccounts{accounts: []config.CloudAccount{awsAccount("123456789012")}}
	probers := []Prober{
		&stubProber{name: "rds", combos: []Combo{{Provider: "aws", Service: "rds", TermYears: 1, Payment: "all-upfront"}}},
		&stubProber{name: "elasticache", combos: []Combo{{Provider: "aws", Service: "elasticache", TermYears: 3, Payment: "no-upfront"}}},
	}
	svc := New(store, accounts, noopBuildConfig, probers)

	got, err := svc.Get(context.Background())
	require.NoError(t, err)
	require.Contains(t, got, "aws")
	assert.Len(t, got["aws"]["rds"], 1)
	assert.Len(t, got["aws"]["elasticache"], 1)
	assert.Equal(t, int32(1), atomic.LoadInt32(&store.saves))
	assert.Equal(t, "123456789012", store.savedID)
	assert.Equal(t, 2, store.savedCnt)
}

func TestService_Get_OneProberFailsDoesNotPersist(t *testing.T) {
	store := &fakeStore{}
	accounts := &fakeAccounts{accounts: []config.CloudAccount{awsAccount("a")}}
	boom := errors.New("boom")
	probers := []Prober{
		&stubProber{name: "rds", combos: []Combo{{Provider: "aws", Service: "rds", TermYears: 1, Payment: "all-upfront"}}},
		&stubProber{name: "elasticache", err: boom},
	}
	svc := New(store, accounts, noopBuildConfig, probers)

	_, err := svc.Get(context.Background())
	assert.ErrorIs(t, err, ErrNoData)
	assert.Zero(t, atomic.LoadInt32(&store.saves), "partial probe results must NOT be persisted")
}

func TestService_Get_BuildConfigErrorReturnsErrNoData(t *testing.T) {
	store := &fakeStore{}
	accounts := &fakeAccounts{accounts: []config.CloudAccount{awsAccount("a")}}
	boom := errors.New("creds")
	svc := New(store, accounts, func(context.Context, *config.CloudAccount) (aws.Config, error) {
		return aws.Config{}, boom
	}, []Prober{&stubProber{name: "rds"}})

	_, err := svc.Get(context.Background())
	assert.ErrorIs(t, err, ErrNoData)
	assert.Zero(t, atomic.LoadInt32(&store.saves))
}

func TestService_Get_StoreReadErrorBubbles(t *testing.T) {
	boom := errors.New("db down")
	store := &fakeStore{getErr: boom}
	accounts := &fakeAccounts{accounts: []config.CloudAccount{awsAccount("a")}}
	svc := New(store, accounts, noopBuildConfig, []Prober{&stubProber{name: "rds"}})

	_, err := svc.Get(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

func TestService_Validate_PermissiveOnErrNoData(t *testing.T) {
	// No AWS account → Get returns ErrNoData → Validate must return true.
	store := &fakeStore{}
	accounts := &fakeAccounts{accounts: nil}
	svc := New(store, accounts, noopBuildConfig, nil)

	ok, err := svc.Validate(context.Background(), "aws", "rds", 1, "all-upfront")
	require.NoError(t, err)
	assert.True(t, ok, "missing data must not block saves")
}

func TestService_Validate_HitAndMiss(t *testing.T) {
	cached := Options{"aws": {"rds": {
		{Provider: "aws", Service: "rds", TermYears: 1, Payment: "all-upfront"},
		{Provider: "aws", Service: "rds", TermYears: 3, Payment: "no-upfront"},
	}}}
	store := &fakeStore{opts: cached, has: true}
	accounts := &fakeAccounts{}
	svc := New(store, accounts, noopBuildConfig, nil)

	ok, err := svc.Validate(context.Background(), "aws", "rds", 1, "all-upfront")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = svc.Validate(context.Background(), "aws", "rds", 3, "partial-upfront")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestService_Validate_UnknownProviderPermissive(t *testing.T) {
	cached := Options{"aws": {"rds": {{Provider: "aws", Service: "rds", TermYears: 1, Payment: "all-upfront"}}}}
	store := &fakeStore{opts: cached, has: true}
	svc := New(store, &fakeAccounts{}, noopBuildConfig, nil)

	// Azure isn't in our probe set — don't block.
	ok, err := svc.Validate(context.Background(), "azure", "vm", 1, "all-upfront")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestService_Validate_UnknownServiceUnderKnownProviderPermissive(t *testing.T) {
	cached := Options{"aws": {"rds": {{Provider: "aws", Service: "rds", TermYears: 1, Payment: "all-upfront"}}}}
	store := &fakeStore{opts: cached, has: true}
	svc := New(store, &fakeAccounts{}, noopBuildConfig, nil)

	// savingsplans has no probe — don't block.
	ok, err := svc.Validate(context.Background(), "aws", "savingsplans", 1, "all-upfront")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestService_Get_ConcurrentCallersProbeOnce(t *testing.T) {
	// Serialize probes via the mutex: N concurrent Get()s on a cold store
	// must result in exactly one Save.
	store := &fakeStore{}
	accounts := &fakeAccounts{accounts: []config.CloudAccount{awsAccount("a")}}

	var probeCount int32
	probers := []Prober{proberFunc{name: "rds", fn: func() ([]Combo, error) {
		atomic.AddInt32(&probeCount, 1)
		return []Combo{{Provider: "aws", Service: "rds", TermYears: 1, Payment: "all-upfront"}}, nil
	}}}
	svc := New(store, accounts, noopBuildConfig, probers)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.Get(context.Background())
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), atomic.LoadInt32(&store.saves), "Save must run exactly once")
	assert.Equal(t, int32(1), atomic.LoadInt32(&probeCount), "Probe must run exactly once for 8 concurrent callers")
}

// proberFunc is a tiny adapter letting tests wire a closure as a Prober.
type proberFunc struct {
	name string
	fn   func() ([]Combo, error)
}

func (p proberFunc) Service() string { return p.name }
func (p proberFunc) Probe(ctx context.Context, _ aws.Config) ([]Combo, error) {
	return p.fn()
}

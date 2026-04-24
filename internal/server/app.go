// Package server provides a cloud-agnostic server implementation for CUDly.
// It supports both AWS Lambda and standard HTTP server modes.
package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LeanerCloud/CUDly/internal/analytics"
	"github.com/LeanerCloud/CUDly/internal/api"
	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/commitmentopts"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/database"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/internal/oidc"
	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/internal/runtime"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
	"github.com/LeanerCloud/CUDly/internal/secrets"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Application holds all components of the CUDly server
type Application struct {
	Config      config.StoreInterface
	API         *api.Handler
	Scheduler   SchedulerInterface
	Purchase    PurchaseManagerInterface
	Email       email.SenderInterface // Multi-cloud email sender (AWS SES, GCP SendGrid, Azure ACS)
	Auth        *auth.Service
	RateLimiter api.RateLimiterInterface // Distributed rate limiter (DB-backed for multi-instance)
	Analytics   AnalyticsStoreInterface  // Analytics store for savings data
	Version     string
	DB          *database.Connection // PostgreSQL database connection
	TaskLocker  TaskLocker           // Advisory lock for scheduled tasks (defaults to DB)

	// Static file serving directory (from STATIC_DIR env var)
	staticDir string

	// Lazy initialization fields for PostgreSQL (Lambda ENI readiness)
	dbConfig       *database.Config
	secretResolver secrets.Resolver
	dbMu           sync.Mutex
	dbConnected    bool
	dbErr          error
	appConfig      ApplicationConfig

	// State from the most recent migration attempt. Surfaced by /health so
	// ops can see failures. Protected by its OWN dedicated mutex — NOT
	// dbMu, because ensureDB holds dbMu for its full duration. If /health
	// reached into dbMu it would block behind a long-running ensureDB,
	// which defeats the point of non-fatal migrations.
	migrationErr        error
	migrationFinishedAt time.Time
	migrationMu         sync.Mutex

	// OIDC signer (optional, backs /.well-known/* and the Azure
	// federated credential path). Nil when the deployment has not
	// opted into the federated flow.
	signer oidc.Signer
}

// ApplicationConfig holds all env-based configuration for the application
type ApplicationConfig struct {
	Version                string
	NotificationDaysBefore int
	DefaultTerm            int
	DefaultPaymentOption   string
	DefaultCoverage        float64
	DefaultRampSchedule    string
	APIKeySecretARN        string
	EnableDashboard        bool
	DashboardBucket        string
	DashboardURL           string
	// IssuerURL is the canonical OIDC issuer URL published under
	// /.well-known/* and used as the iss claim in JWTs minted by the
	// KMS-backed signer. Falls back to DashboardURL. Set via the
	// CUDLY_ISSUER_URL env var; in the AWS Lambda deploy the Terraform
	// module wires this to the Function URL so the deployment is
	// self-contained without needing a frontend domain.
	IssuerURL         string
	CORSAllowedOrigin string
	// ScheduledTaskSecret is the shared secret checked on the /scheduled
	// endpoint. In production (Azure Container Apps, Lambda-with-KV) it is
	// resolved lazily from SCHEDULED_TASK_SECRET_NAME via the SecretResolver
	// in NewApplicationFromDeps, so the value never lives in a container
	// env var. In dev the plaintext SCHEDULED_TASK_SECRET env var is still
	// accepted as a fallback.
	ScheduledTaskSecret     string
	ScheduledTaskSecretName string
	IsLambda                bool
}

// ExternalDeps holds pre-built external dependencies that require infrastructure
type ExternalDeps struct {
	EmailSender    email.SenderInterface
	ConfigStore    config.StoreInterface
	DBConfig       *database.Config
	SecretResolver secrets.Resolver
	STSClient      purchase.STSClient
}

// isLambdaRuntime is a thin wrapper over runtime.IsLambda so existing
// call sites stay unchanged. New code should call runtime.IsLambda
// directly.
func isLambdaRuntime() bool {
	return runtime.IsLambda()
}

// defaultMigrationsTimeout bounds how long ensureDB waits for migrations
// before giving up and proceeding. Deliberately shorter than the default
// Lambda timeout (30s at this writing) so a runaway migration gets
// cancelled cleanly inside ensureDB rather than by Lambda mid-invocation
// (which is exactly what leaves schema_migrations.dirty = true).
const defaultMigrationsTimeout = 20 * time.Second

// migrationsTimeout is resolved once at package init time from
// CUDLY_MIGRATION_TIMEOUT (time.ParseDuration). Declared as a var (not
// const) so tests can overwrite it inside t.Cleanup to exercise the
// timeout path with a 50ms budget. Tests that overwrite this MUST NOT
// call t.Parallel() since there's no synchronisation on the variable.
var migrationsTimeout = resolveMigrationsTimeout()

func resolveMigrationsTimeout() time.Duration {
	v := os.Getenv("CUDLY_MIGRATION_TIMEOUT")
	if v == "" {
		return defaultMigrationsTimeout
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		log.Printf("CUDLY_MIGRATION_TIMEOUT invalid (%q); using default %s", v, defaultMigrationsTimeout)
		return defaultMigrationsTimeout
	}
	return d
}

// runMigrations is a package-level indirection so tests can swap in a fake
// that returns error / hangs / succeeds without running real SQL. Same
// parallel-test restriction as migrationsTimeout.
var runMigrations = migrations.RunMigrations

// recordMigrationResult stores the outcome of the most recent migration
// attempt. Takes migrationMu briefly. Called from inside ensureDB which
// holds dbMu — lock order is dbMu then migrationMu. Never take them the
// other way around.
func (app *Application) recordMigrationResult(err error) {
	app.migrationMu.Lock()
	defer app.migrationMu.Unlock()
	app.migrationErr = err
	app.migrationFinishedAt = time.Now()
}

// snapshotMigrationState returns a point-in-time copy of the migration
// state suitable for /health rendering.
func (app *Application) snapshotMigrationState() (err error, finishedAt time.Time) {
	app.migrationMu.Lock()
	defer app.migrationMu.Unlock()
	return app.migrationErr, app.migrationFinishedAt
}

// runMigrationsBounded runs the package-level runMigrations hook in a
// goroutine bounded by timeout. The returned error is either the runner's
// own error, a panic wrapped as an error, or a timeout error — never a
// nil-with-goroutine-still-alive. The goroutine is guaranteed to have
// exited before this function returns (the timeout branch waits on
// <-done after cancelling the ctx), so no orphan goroutine survives past
// this call — critical on Lambda where goroutines freeze between
// invocations.
func runMigrationsBounded(pool *pgxpool.Pool, migrationsPath, adminEmail, adminPassword string, timeout time.Duration) error {
	migCtx, cancelMig := context.WithTimeout(context.Background(), timeout)
	defer cancelMig()

	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("migration panic: %v", r)
			}
		}()
		done <- runMigrations(migCtx, pool, migrationsPath, adminEmail, adminPassword)
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		cancelMig()
		<-done
		return fmt.Errorf("migration timed out after %s", timeout)
	}
}

// resolveOIDCIssuerURL picks the canonical OIDC issuer URL for the
// deployment. CUDLY_ISSUER_URL (set by the infra module to the
// Function URL / Container App URL / Cloud Run URL) wins; DashboardURL
// is the backstop.
func resolveOIDCIssuerURL(cfg ApplicationConfig) string {
	if cfg.IssuerURL != "" {
		return strings.TrimRight(cfg.IssuerURL, "/")
	}
	return strings.TrimRight(cfg.DashboardURL, "/")
}

// LoadApplicationConfig reads all configuration from environment variables
func LoadApplicationConfig() ApplicationConfig {
	version := os.Getenv("VERSION")
	if version == "" {
		version = "dev"
	}

	return ApplicationConfig{
		Version:                version,
		NotificationDaysBefore: getEnvInt("NOTIFICATION_DAYS_BEFORE", 3),
		DefaultTerm:            getEnvInt("DEFAULT_TERM", 3),
		DefaultPaymentOption:   os.Getenv("DEFAULT_PAYMENT_OPTION"),
		DefaultCoverage:        getEnvFloat("DEFAULT_COVERAGE", 80),
		DefaultRampSchedule:    os.Getenv("DEFAULT_RAMP_SCHEDULE"),
		APIKeySecretARN:        os.Getenv("API_KEY_SECRET_ARN"),
		EnableDashboard:        os.Getenv("ENABLE_DASHBOARD") == "true",
		DashboardBucket:        os.Getenv("DASHBOARD_BUCKET"),
		DashboardURL:           os.Getenv("DASHBOARD_URL"),
		IssuerURL:              os.Getenv("CUDLY_ISSUER_URL"),
		CORSAllowedOrigin:      os.Getenv("CORS_ALLOWED_ORIGIN"),
		// SCHEDULED_TASK_SECRET (plaintext) is the legacy dev-only path.
		// SCHEDULED_TASK_SECRET_NAME (secret-store name) is preferred in prod;
		// NewApplicationFromDeps resolves it via the SecretResolver at init.
		ScheduledTaskSecret:     os.Getenv("SCHEDULED_TASK_SECRET"),
		ScheduledTaskSecretName: os.Getenv("SCHEDULED_TASK_SECRET_NAME"),
		IsLambda:                isLambdaRuntime(),
	}
}

// resolveScheduledTaskSecret resolves SCHEDULED_TASK_SECRET_NAME to its real
// value via the configured SecretResolver (Azure Key Vault / AWS Secrets
// Manager) when possible. Falls back to cfg.ScheduledTaskSecret (plaintext
// SCHEDULED_TASK_SECRET env var) if the resolver is absent or the lookup
// fails. Pulled out of NewApplicationFromDeps to keep it under the
// cyclomatic limit.
func resolveScheduledTaskSecret(ctx context.Context, cfg ApplicationConfig, resolver secrets.Resolver) string {
	if cfg.ScheduledTaskSecretName == "" || cfg.ScheduledTaskSecret != "" || resolver == nil {
		return cfg.ScheduledTaskSecret
	}
	resolved, err := resolver.GetSecret(ctx, cfg.ScheduledTaskSecretName)
	if err != nil {
		log.Printf("scheduled task secret resolution failed for %q: %v (falling back to SCHEDULED_TASK_SECRET)", cfg.ScheduledTaskSecretName, err)
		return cfg.ScheduledTaskSecret
	}
	return resolved
}

// NewApplicationFromDeps creates an Application from pre-built configuration and dependencies.
// This is the testable constructor - all external I/O is done before calling this.
func NewApplicationFromDeps(ctx context.Context, cfg ApplicationConfig, deps ExternalDeps) (*Application, error) {
	if deps.DBConfig == nil {
		return nil, fmt.Errorf("database configuration required: DBConfig must be provided")
	}

	cfg.ScheduledTaskSecret = resolveScheduledTaskSecret(ctx, cfg, deps.SecretResolver)

	// Construct the OIDC issuer signer once per deployment. Nil means
	// the deployment has not opted into the federated flow yet — all
	// OIDC-dependent paths (handler_oidc.go, purchase manager Azure
	// federated credential) fall back to their legacy behaviours.
	signer, signerErr := oidc.NewSignerFromEnv(ctx)
	if signerErr != nil {
		log.Printf("oidc signer init failed (federated flow disabled): %v", signerErr)
		signer = nil
	}

	// Prime the issuer URL cache. Priority order:
	//  1. CUDLY_ISSUER_URL / DASHBOARD_URL via resolveOIDCIssuerURL
	//  2. AWS Lambda self-lookup via lambda:GetFunctionUrlConfig
	//
	// The handler_oidc.go path is still a backstop (populates the
	// cache from the first inbound request's DomainName), but doing
	// it here means scheduled-task cold starts don't race the first
	// inbound HTTP request.
	if issuer := resolveOIDCIssuerURL(cfg); issuer != "" {
		oidc.SetIssuerURL(issuer)
	} else if cfg.IsLambda {
		primeCtx, primeCancel := context.WithTimeout(ctx, 5*time.Second)
		if err := oidc.PrimeIssuerURLFromLambda(primeCtx); err != nil {
			log.Printf("oidc issuer cache not primed from lambda: %v (falling back to request-driven populate)", err)
		}
		primeCancel()
	}

	// Initialize purchase manager
	purchaseManager := purchase.NewManager(purchase.ManagerConfig{
		ConfigStore:            deps.ConfigStore,
		EmailSender:            deps.EmailSender,
		STSClient:              deps.STSClient,
		NotificationDaysBefore: cfg.NotificationDaysBefore,
		DefaultTerm:            cfg.DefaultTerm,
		DefaultPaymentOption:   cfg.DefaultPaymentOption,
		DefaultCoverage:        cfg.DefaultCoverage,
		DefaultRampSchedule:    cfg.DefaultRampSchedule,
		OIDCSigner:             signer,
		OIDCIssuerURL:          resolveOIDCIssuerURL(cfg),
	})

	// Initialize scheduler
	sched := scheduler.NewScheduler(scheduler.SchedulerConfig{
		ConfigStore:     deps.ConfigStore,
		PurchaseManager: purchaseManager,
		EmailSender:     deps.EmailSender,
		IsLambda:        isLambdaRuntime(),
	})

	// Auth store will be initialized lazily after DB connection
	var authStore auth.StoreInterface
	log.Println("PostgreSQL auth store will be initialized on first request")

	// Initialize auth service
	authService := auth.NewService(auth.ServiceConfig{
		Store:           authStore,
		EmailSender:     deps.EmailSender,
		SessionDuration: 24 * time.Hour,
		DashboardURL:    cfg.DashboardURL,
	})

	// Initialize rate limiter based on runtime environment
	var rateLimiter api.RateLimiterInterface
	if !cfg.IsLambda {
		rateLimiter = api.NewInMemoryRateLimiter()
		log.Println("Initialized in-memory rate limiter for single-instance deployment (Fargate/Container)")
	} else {
		log.Println("Lambda runtime detected - database rate limiter will be initialized on first request")
	}

	// Initialize API handler
	apiHandler := api.NewHandler(api.HandlerConfig{
		ConfigStore:       deps.ConfigStore,
		PurchaseManager:   purchaseManager,
		Scheduler:         sched,
		AuthService:       newAuthServiceAdapter(authService),
		APIKeySecretARN:   cfg.APIKeySecretARN,
		EnableDashboard:   cfg.EnableDashboard,
		DashboardBucket:   cfg.DashboardBucket,
		CORSAllowedOrigin: cfg.CORSAllowedOrigin,
		RateLimiter:       rateLimiter,
		EmailNotifier:     deps.EmailSender,
		DashboardURL:      cfg.DashboardURL,
		OIDCSigner:        signer,
		OIDCIssuerURL:     resolveOIDCIssuerURL(cfg),
	})

	log.Printf("CUDly Server initialization complete")

	return &Application{
		Config:         deps.ConfigStore,
		API:            apiHandler,
		Scheduler:      sched,
		Purchase:       purchaseManager,
		Email:          deps.EmailSender,
		Auth:           authService,
		RateLimiter:    rateLimiter,
		Version:        cfg.Version,
		DB:             nil, // Will be initialized lazily on first request
		staticDir:      staticDirFromEnv(),
		dbConfig:       deps.DBConfig,
		secretResolver: deps.SecretResolver,
		appConfig:      cfg,
		signer:         signer,
	}, nil
}

// NewApplication creates and initializes a new Application instance
func NewApplication(ctx context.Context) (*Application, error) {
	cfg := LoadApplicationConfig()

	log.Printf("CUDly Server initializing, version: %s", cfg.Version)

	// Initialize configuration store (PostgreSQL)
	configStore, dbConfig, secretResolver, err := initConfigStore(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize config store: %w", err)
	}

	// Initialize email sender (auto-detects cloud provider from SECRET_PROVIDER env var)
	emailSender, err := email.NewSenderFromEnvironment(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize email sender: %w", err)
	}

	// Initialize AWS config for STS client
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}
	stsClient := sts.NewFromConfig(awsCfg)

	deps := ExternalDeps{
		EmailSender:    emailSender,
		ConfigStore:    configStore,
		DBConfig:       dbConfig,
		SecretResolver: secretResolver,
		STSClient:      stsClient,
	}

	return NewApplicationFromDeps(ctx, cfg, deps)
}

// ensureDB ensures the database connection is established (lazy initialization).
// This is called on first request to ensure Lambda ENI is ready.
// Unlike sync.Once, transient failures allow retry on subsequent requests.
func (app *Application) ensureDB(ctx context.Context) error {
	// Only attempt lazy init if we have a dbConfig
	if app.dbConfig == nil {
		return nil // Not using PostgreSQL
	}

	app.dbMu.Lock()
	defer app.dbMu.Unlock()

	// Already connected successfully
	if app.dbConnected {
		return nil
	}

	// If a previous error was set (e.g. in tests), return it
	if app.dbErr != nil {
		return app.dbErr
	}

	log.Println("Establishing PostgreSQL connection (lazy initialization)...")

	// Connect to PostgreSQL
	dbConn, err := database.NewConnection(ctx, app.dbConfig, app.secretResolver)
	if err != nil {
		return fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	// Store the connection
	app.DB = dbConn
	log.Println("PostgreSQL connection established successfully")

	// Run migrations if AutoMigrate is enabled. Failures are non-fatal:
	// we log, surface via /health's migrations check, and proceed. The app
	// stays up; handlers that need the missing schema error at query time.
	// See specs/migration-resilience.md (or the plan) for the rationale.
	if app.dbConfig.AutoMigrate {
		log.Println("Running database migrations...")

		adminEmail := os.Getenv("ADMIN_EMAIL")
		adminPassword, err := app.resolveAdminPassword(ctx)
		if err != nil {
			return err // secret-resolution failure is still fatal — env/config, not a migration runtime error
		}

		migErr := runMigrationsBounded(dbConn.Pool(), app.dbConfig.MigrationsPath, adminEmail, adminPassword, migrationsTimeout)
		app.recordMigrationResult(migErr)

		if migErr != nil {
			log.Printf("⚠️ Migration failed — app continuing with existing schema: %v", migErr)
			// Intentionally do NOT return, do NOT close dbConn, do NOT clear app.DB.
		} else {
			log.Println("Database migrations completed successfully")
		}
	}

	// Re-initialize all stores and services with the live DB connection
	if err := app.reinitializeAfterConnect(dbConn); err != nil {
		dbConn.Close()
		app.DB = nil
		return fmt.Errorf("failed to reinitialize after DB connect: %w", err)
	}
	app.dbConnected = true

	return nil
}

// resolveAdminPassword returns the admin password from Secrets Manager.
// It requires ADMIN_PASSWORD_SECRET to be set to a valid secret ARN/name.
// If absent, it returns an error to prevent startup with no secret source.
func (app *Application) resolveAdminPassword(ctx context.Context) (string, error) {
	secret := os.Getenv("ADMIN_PASSWORD_SECRET")
	if secret == "" {
		return "", fmt.Errorf("ADMIN_PASSWORD_SECRET environment variable is required but not set; refusing to start without a Secrets Manager ARN")
	}
	if app.secretResolver == nil {
		return "", fmt.Errorf("secret resolver is not configured; cannot resolve ADMIN_PASSWORD_SECRET")
	}
	resolved, err := app.secretResolver.GetSecret(ctx, secret)
	if err != nil {
		return "", fmt.Errorf("failed to resolve admin password secret: %w", err)
	}
	return resolved, nil
}

// reinitializeAfterConnect re-creates all stores and services that depend on the
// database connection. This is called after the lazy DB connect succeeds.
// Returns an error if any store or service initialization fails.
func (app *Application) reinitializeAfterConnect(dbConn *database.Connection) error {
	// Initialize config store with the connection
	pgStore := config.NewPostgresStore(dbConn)
	if pgStore == nil {
		return fmt.Errorf("failed to create PostgreSQL config store")
	}
	app.Config = pgStore

	// Initialize auth store with the connection
	authStore := auth.NewPostgresStore(dbConn)
	if authStore == nil {
		return fmt.Errorf("failed to create PostgreSQL auth store")
	}

	// Update auth service with PostgreSQL auth store
	app.Auth = auth.NewService(auth.ServiceConfig{
		Store:            authStore,
		EmailSender:      app.Email,
		SessionDuration:  24 * time.Hour,
		DashboardURL:     app.appConfig.DashboardURL,
		OnPasswordChange: buildAdminPasswordSyncCallback(authStore, app.secretResolver),
	})
	if app.Auth == nil {
		return fmt.Errorf("failed to create auth service")
	}

	// Initialize distributed rate limiter for Lambda (multi-instance)
	// For Fargate/containers, we already have in-memory rate limiter from startup
	if app.appConfig.IsLambda {
		app.RateLimiter = api.NewDBRateLimiter(dbConn.Pool())
		log.Println("Initialized database-backed rate limiter for Lambda (distributed state)")
	}

	// Initialize analytics store for savings data and materialized views
	app.Analytics = analytics.NewPostgresAnalyticsStore(dbConn)
	log.Println("Initialized PostgreSQL analytics store")

	// Initialize credential store (AES-256-GCM encrypted credential blobs)
	encKey, err := credentials.KeyFromEnv()
	if err != nil {
		return fmt.Errorf("failed to load credential encryption key: %w", err)
	}
	credStore := credentials.NewCredentialStore(dbConn.Pool(), encKey)
	log.Println("Initialized encrypted credential store")

	// Re-initialize purchase manager with multi-account deps now that credStore is available.
	// The initial manager (created before DB connect) lacks CredentialStore and AssumeRoleSTS,
	// so the multi-account fan-out guard (m.credStore != nil) would always be false without this.
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		return fmt.Errorf("failed to load AWS config for cross-account STS: %w", err)
	}
	app.Purchase = purchase.NewManager(purchase.ManagerConfig{
		ConfigStore:            app.Config,
		EmailSender:            app.Email,
		STSClient:              sts.NewFromConfig(awsCfg),
		AssumeRoleSTS:          sts.NewFromConfig(awsCfg),
		CredentialStore:        credStore,
		NotificationDaysBefore: app.appConfig.NotificationDaysBefore,
		DefaultTerm:            app.appConfig.DefaultTerm,
		DefaultPaymentOption:   app.appConfig.DefaultPaymentOption,
		DefaultCoverage:        app.appConfig.DefaultCoverage,
		DefaultRampSchedule:    app.appConfig.DefaultRampSchedule,
		DashboardURL:           app.appConfig.DashboardURL,
		OIDCSigner:             app.signer,
		OIDCIssuerURL:          resolveOIDCIssuerURL(app.appConfig),
	})
	log.Println("Re-initialized purchase manager with credential store and cross-account STS")

	// Re-initialize scheduler with per-account credential resolution.
	app.Scheduler = scheduler.NewScheduler(scheduler.SchedulerConfig{
		ConfigStore:     app.Config,
		PurchaseManager: app.Purchase,
		EmailSender:     app.Email,
		CredentialStore: credStore,
		OIDCSigner:      app.signer,
		OIDCIssuerURL:   resolveOIDCIssuerURL(app.appConfig),
		AssumeRoleSTS:   sts.NewFromConfig(awsCfg),
		IsLambda:        app.appConfig.IsLambda,
	})

	// Build the commitment-options probe/cache service. It lazily probes
	// AWS reserved-offerings APIs through the first enabled AWS account and
	// persists the result so subsequent calls come from the DB. Failures
	// anywhere along the chain (no account connected, probe denied) collapse
	// to ErrNoData; the API handler and save-side validator both degrade
	// gracefully, so this is wired unconditionally.
	commitmentOpts := commitmentopts.New(
		commitmentopts.NewPostgresStore(dbConn),
		app.Config,
		func(ctx context.Context, acct *config.CloudAccount) (aws.Config, error) {
			stsClient := sts.NewFromConfig(awsCfg)
			prov, err := credentials.ResolveAWSCredentialProvider(ctx, acct, credStore, stsClient)
			if err != nil {
				return aws.Config{}, err
			}
			// us-east-1 hardcoded because reserved offerings are global
			// facts (not AZ-scoped), and us-east-1 has the widest
			// instance-type coverage. This fails silently for GovCloud
			// / China-partition accounts — those return ErrNoData from
			// the probe and the frontend falls back to hardcoded rules,
			// which is acceptable since those partitions rarely need
			// dynamic commitment detection.
			return awsconfig.LoadDefaultConfig(ctx,
				awsconfig.WithCredentialsProvider(prov),
				awsconfig.WithRegion("us-east-1"),
			)
		},
		commitmentopts.DefaultProbers(),
	)

	// Update API handler with new config store, scheduler, and rate limiter.
	// AnalyticsClient is Postgres-backed (see api.NewPostgresAnalyticsClient) —
	// it aggregates purchase_history on demand so the History UI charts work
	// without requiring a separate S3/Athena deployment.
	app.API = api.NewHandler(api.HandlerConfig{
		ConfigStore:       app.Config,
		CredentialStore:   credStore,
		PurchaseManager:   app.Purchase,
		Scheduler:         app.Scheduler,
		AuthService:       newAuthServiceAdapter(app.Auth),
		APIKeySecretARN:   app.appConfig.APIKeySecretARN,
		EnableDashboard:   app.appConfig.EnableDashboard,
		DashboardBucket:   app.appConfig.DashboardBucket,
		CORSAllowedOrigin: app.appConfig.CORSAllowedOrigin,
		RateLimiter:       app.RateLimiter,
		EmailNotifier:     app.Email,
		DashboardURL:      app.appConfig.DashboardURL,
		AnalyticsClient:   api.NewPostgresAnalyticsClient(dbConn),
		OIDCSigner:        app.signer,
		OIDCIssuerURL:     resolveOIDCIssuerURL(app.appConfig),
		CommitmentOpts:    commitmentOpts,
	})
	if app.API == nil {
		return fmt.Errorf("failed to create API handler")
	}

	return nil
}

// buildAdminPasswordSyncCallback returns a callback that syncs the admin user's
// password to the secret manager when it changes. Returns nil if the required
// environment variables (ADMIN_PASSWORD_SECRET, ADMIN_EMAIL) are not set or
// the secret resolver is nil.
func buildAdminPasswordSyncCallback(store auth.StoreInterface, resolver secrets.Resolver) func(ctx context.Context, userID, newPassword string) {
	adminPasswordSecret := os.Getenv("ADMIN_PASSWORD_SECRET")
	adminEmail := os.Getenv("ADMIN_EMAIL")

	if adminPasswordSecret == "" || resolver == nil || adminEmail == "" {
		return nil
	}

	return func(ctx context.Context, userID, newPassword string) {
		user, err := store.GetUserByID(ctx, userID)
		if err != nil || user == nil || user.Email != adminEmail {
			return
		}
		if err := resolver.PutSecret(ctx, adminPasswordSecret, newPassword); err != nil {
			logging.Warnf("Failed to sync admin password to secret manager: %v", err)
		} else {
			logging.Infof("Admin password synced to secret manager")
		}
	}
}

// Close gracefully shuts down the application
func (app *Application) Close() error {
	log.Println("Shutting down CUDly Server...")

	// Close database connection if using PostgreSQL
	if app.DB != nil {
		log.Println("Closing database connection...")
		app.DB.Close()
		log.Println("Database connection closed successfully")
	}

	return nil
}

// initConfigStore initializes the configuration store using PostgreSQL
// Connection is deferred (lazy init) until first request to avoid Lambda ENI issues
func initConfigStore(ctx context.Context) (config.StoreInterface, *database.Config, secrets.Resolver, error) {
	// Require PostgreSQL configuration
	if os.Getenv("DB_HOST") == "" {
		return nil, nil, nil, fmt.Errorf("database configuration required: DB_HOST must be set")
	}

	log.Println("Preparing PostgreSQL configuration store (lazy initialization)...")

	// Initialize secret resolver
	secretResolver, err := secrets.NewResolver(ctx, secrets.LoadConfigFromEnv())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create secret resolver: %w", err)
	}

	// Load database config from environment
	dbConfig, err := database.LoadFromEnv()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to load database config: %w", err)
	}

	log.Printf("PostgreSQL config loaded (will connect on first request): %s:%d", dbConfig.Host, dbConfig.Port)

	// Return nil for config store - will be created lazily
	// This avoids connecting during Lambda init when ENI isn't ready
	return nil, dbConfig, secretResolver, nil
}

// Helper functions for environment variable parsing

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if result, err := strconv.Atoi(val); err == nil {
			return result
		}
	}
	return defaultVal
}

func getEnvFloat(key string, defaultVal float64) float64 {
	if val := os.Getenv(key); val != "" {
		if result, err := strconv.ParseFloat(val, 64); err == nil {
			return result
		}
	}
	return defaultVal
}

// authServiceAdapter adapts auth.Service to api.AuthServiceInterface
type authServiceAdapter struct {
	service *auth.Service
}

func newAuthServiceAdapter(service *auth.Service) *authServiceAdapter {
	return &authServiceAdapter{service: service}
}

func (a *authServiceAdapter) Login(ctx context.Context, req api.LoginRequest) (*api.LoginResponse, error) {
	authReq := auth.LoginRequest{
		Email:    req.Email,
		Password: req.Password,
		MFACode:  req.MFACode,
	}
	resp, err := a.service.Login(ctx, authReq)
	if err != nil {
		return nil, err
	}
	return &api.LoginResponse{
		Token:     resp.Token,
		ExpiresAt: resp.ExpiresAt.Format(time.RFC3339),
		User: &api.UserInfo{
			ID:         resp.User.ID,
			Email:      resp.User.Email,
			Role:       resp.User.Role,
			Groups:     resp.User.Groups,
			MFAEnabled: resp.User.MFAEnabled,
		},
		CSRFToken: resp.CSRFToken,
	}, nil
}

func (a *authServiceAdapter) Logout(ctx context.Context, token string) error {
	return a.service.Logout(ctx, token)
}

func (a *authServiceAdapter) ValidateSession(ctx context.Context, token string) (*api.Session, error) {
	sess, err := a.service.ValidateSession(ctx, token)
	if err != nil {
		return nil, err
	}
	return &api.Session{
		UserID: sess.UserID,
		Email:  sess.Email,
		Role:   sess.Role,
	}, nil
}

func (a *authServiceAdapter) SetupAdmin(ctx context.Context, req api.SetupAdminRequest) (*api.LoginResponse, error) {
	authReq := auth.SetupAdminRequest{
		Email:    req.Email,
		Password: req.Password,
	}
	resp, err := a.service.SetupAdmin(ctx, authReq)
	if err != nil {
		return nil, err
	}
	return &api.LoginResponse{
		Token:     resp.Token,
		ExpiresAt: resp.ExpiresAt.Format(time.RFC3339),
		User: &api.UserInfo{
			ID:         resp.User.ID,
			Email:      resp.User.Email,
			Role:       resp.User.Role,
			Groups:     resp.User.Groups,
			MFAEnabled: resp.User.MFAEnabled,
		},
		CSRFToken: resp.CSRFToken,
	}, nil
}

func (a *authServiceAdapter) CheckAdminExists(ctx context.Context) (bool, error) {
	return a.service.CheckAdminExists(ctx)
}

func (a *authServiceAdapter) RequestPasswordReset(ctx context.Context, email string) error {
	return a.service.RequestPasswordReset(ctx, email)
}

func (a *authServiceAdapter) ConfirmPasswordReset(ctx context.Context, req api.PasswordResetConfirm) error {
	authReq := auth.PasswordResetConfirm{
		Token:       req.Token,
		NewPassword: req.NewPassword,
	}
	return a.service.ConfirmPasswordReset(ctx, authReq)
}

func (a *authServiceAdapter) GetUser(ctx context.Context, userID string) (*api.User, error) {
	user, err := a.service.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}
	return &api.User{
		ID:         user.ID,
		Email:      user.Email,
		Role:       user.Role,
		MFAEnabled: user.MFAEnabled,
	}, nil
}

func (a *authServiceAdapter) UpdateUserProfile(ctx context.Context, userID string, email string, currentPassword string, newPassword string) error {
	return a.service.UpdateUserProfile(ctx, userID, email, currentPassword, newPassword)
}

// User management methods - delegate to auth service API methods
func (a *authServiceAdapter) CreateUserAPI(ctx context.Context, req any) (any, error) {
	return a.service.CreateUserAPI(ctx, req)
}

func (a *authServiceAdapter) UpdateUserAPI(ctx context.Context, userID string, req any) (any, error) {
	return a.service.UpdateUserAPI(ctx, userID, req)
}

func (a *authServiceAdapter) DeleteUser(ctx context.Context, userID string) error {
	return a.service.DeleteUser(ctx, userID)
}

func (a *authServiceAdapter) ListUsersAPI(ctx context.Context) (any, error) {
	return a.service.ListUsersAPI(ctx)
}

func (a *authServiceAdapter) ChangePasswordAPI(ctx context.Context, userID, currentPassword, newPassword string) error {
	return a.service.ChangePasswordAPI(ctx, userID, currentPassword, newPassword)
}

// Group management methods - delegate to auth service API methods
func (a *authServiceAdapter) CreateGroupAPI(ctx context.Context, req any) (any, error) {
	return a.service.CreateGroupAPI(ctx, req)
}

func (a *authServiceAdapter) UpdateGroupAPI(ctx context.Context, groupID string, req any) (any, error) {
	return a.service.UpdateGroupAPI(ctx, groupID, req)
}

func (a *authServiceAdapter) DeleteGroup(ctx context.Context, groupID string) error {
	return a.service.DeleteGroup(ctx, groupID)
}

func (a *authServiceAdapter) GetGroupAPI(ctx context.Context, groupID string) (any, error) {
	return a.service.GetGroupAPI(ctx, groupID)
}

func (a *authServiceAdapter) ListGroupsAPI(ctx context.Context) (any, error) {
	return a.service.ListGroupsAPI(ctx)
}

// Permission checking
func (a *authServiceAdapter) HasPermissionAPI(ctx context.Context, userID, action, resource string) (bool, error) {
	return a.service.HasPermissionAPI(ctx, userID, action, resource)
}

// Account access
func (a *authServiceAdapter) GetAllowedAccountsAPI(ctx context.Context, userID string) ([]string, error) {
	authCtx, err := a.service.BuildAuthContext(ctx, userID)
	if err != nil {
		return nil, err
	}
	return authCtx.AllowedAccounts, nil
}

// CSRF validation
func (a *authServiceAdapter) ValidateCSRFToken(ctx context.Context, sessionToken, csrfToken string) error {
	return a.service.ValidateCSRFToken(ctx, sessionToken, csrfToken)
}

// API Key management
func (a *authServiceAdapter) CreateAPIKeyAPI(ctx context.Context, userID string, req any) (any, error) {
	return a.service.CreateAPIKeyAPI(ctx, userID, req)
}

func (a *authServiceAdapter) ListUserAPIKeysAPI(ctx context.Context, userID string) (any, error) {
	return a.service.ListUserAPIKeysAPI(ctx, userID)
}

func (a *authServiceAdapter) DeleteAPIKeyAPI(ctx context.Context, userID, keyID string) error {
	return a.service.DeleteAPIKeyAPI(ctx, userID, keyID)
}

func (a *authServiceAdapter) RevokeAPIKeyAPI(ctx context.Context, userID, keyID string) error {
	return a.service.RevokeAPIKeyAPI(ctx, userID, keyID)
}

func (a *authServiceAdapter) ValidateUserAPIKeyAPI(ctx context.Context, apiKey string) (any, any, error) {
	return a.service.ValidateUserAPIKeyAPI(ctx, apiKey)
}

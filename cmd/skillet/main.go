package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	authn "github.com/mhingston/skillet/internal/auth"
	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/config"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/embedding"
	"github.com/mhingston/skillet/internal/gitstore"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/ingest"
	"github.com/mhingston/skillet/internal/packagebuilder"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/polling"
	"github.com/mhingston/skillet/internal/rerank"
	"github.com/mhingston/skillet/internal/search"
	"github.com/mhingston/skillet/internal/store"
)

func main() {
	configPath := flag.String("config", "skillet.yaml", "configuration file")
	flag.Parse()
	c, err := config.Load(*configPath)
	if err != nil {
		slog.Error("configuration failed", "error", err)
		os.Exit(2)
	}
	if err := os.MkdirAll(c.Server.DataDir, 0700); err != nil {
		slog.Error("create data directory", "error", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(ctx, filepath.Join(c.Server.DataDir, "catalogue.db"))
	if err != nil {
		slog.Error("database failed", "error", err)
		os.Exit(2)
	}
	defer db.Close()
	ready := &httpserver.Readiness{}
	ready.SQLite.Store(true)
	ready.PackageStore.Store(true)
	ready.LexicalIndex.Store(true)
	if len(c.Repositories) == 0 {
		ready.InitialSync.Store(true)
	}
	packageStore := packagestore.New(filepath.Join(c.Server.DataDir, "packages"))
	catalog := catalogue.New(db, packageStore)
	var app *httpserver.Server
	var embeddingRequests atomic.Uint64
	var embedder search.Embedder
	if c.Search.Embeddings.BaseURL != "" {
		embeddingClient, embeddingErr := embedding.New(embedding.Config{BaseURL: c.Search.Embeddings.BaseURL, Model: c.Search.Embeddings.Model, Dimensions: c.Search.Embeddings.Dimensions, APIKeyEnv: c.Search.Embeddings.APIKeyEnv})
		if embeddingErr != nil {
			slog.Error("embedding configuration failed", "error", embeddingErr)
			os.Exit(2)
		}
		cachedEmbedder, cacheErr := embedding.NewCached(embeddingClient, &catalogue.EmbeddingCache{DB: db}, "openai-compatible", c.Search.Embeddings.Model, c.Search.Embeddings.Dimensions)
		if cacheErr != nil {
			slog.Error("embedding cache configuration failed", "error", cacheErr)
			os.Exit(2)
		}
		cachedEmbedder.SetObserver(func() {
			embeddingRequests.Add(1)
			if app != nil {
				app.ObserveEmbeddingRequest()
			}
		})
		embedder = cachedEmbedder
	}
	index, err := search.New(embedder)
	if err != nil {
		slog.Error("search index failed", "error", err)
		os.Exit(2)
	}
	docs, err := catalog.RoutingDocuments(ctx, c.Organization.ID, c.Search.SearchableMetadataKeys)
	if err != nil {
		slog.Error("load search index failed", "error", err)
		os.Exit(2)
	}
	if err := index.Rebuild(docs); err != nil {
		slog.Error("index document failed", "error", err)
		os.Exit(2)
	}
	shutdownTimeout, _ := time.ParseDuration(c.Server.ShutdownTimeout)
	if shutdownTimeout <= 0 {
		shutdownTimeout = 20 * time.Second
	}
	auth := httpserver.AuthConfig{Mode: c.Auth.Mode, OrganizationID: c.Organization.ID}
	if c.Auth.StaticTokenEnv != "" {
		auth.StaticToken = os.Getenv(c.Auth.StaticTokenEnv)
		if c.Auth.Mode == "static" {
			validator, validateErr := authn.NewStaticBearerValidator(auth.StaticToken, c.Organization.ID, []string{"skills.search", "skills.materialize"})
			if validateErr != nil {
				slog.Error("authentication configuration failed", "error", validateErr)
				os.Exit(2)
			}
			auth.Validator = validator
		}
	}
	if c.Auth.Mode == "oidc" {
		validator, validateErr := authn.NewOIDCValidator(ctx, authn.OIDCConfig{Issuer: c.Auth.Issuer, Audience: c.Auth.Audience, OrganizationClaim: c.Auth.OrganizationClaim, RequiredScopes: []string{"skills.search", "skills.materialize"}, AllowedAlgorithms: []string{"RS256", "RS384", "RS512"}})
		if validateErr != nil {
			slog.Error("OIDC configuration failed", "error", validateErr)
			os.Exit(2)
		}
		auth.Validator = validator
	}
	candidateKey := os.Getenv("SKILLET_CANDIDATE_SIGNING_KEY")
	if candidateKey == "" && c.Auth.Mode == "development" {
		candidateKey = "development-only-candidate-key"
	}
	if candidateKey == "" {
		slog.Error("candidate signing key is required outside development")
		os.Exit(2)
	}
	packageKey := os.Getenv(c.Packages.SigningKeyEnv)
	if packageKey == "" && c.Auth.Mode == "development" {
		packageKey = "development-only-package-key"
	}
	if c.Packages.Enabled && packageKey == "" {
		slog.Error("package signing key is required when packages are enabled")
		os.Exit(2)
	}
	if c.Packages.Enabled {
		app = httpserver.NewComplete(slog.Default(), ready, index, c.Organization.ID, candidate.Signer{Key: []byte(candidateKey)}, packageStore, packageurl.Signer{Key: []byte(packageKey)}, catalog, c.Server.PublicBaseURL)
	} else {
		app = httpserver.NewWithSearch(slog.Default(), ready, index, c.Organization.ID, candidate.Signer{Key: []byte(candidateKey)})
	}
	if packageURLTTL, parseErr := time.ParseDuration(c.Packages.SignedURLTTL); parseErr == nil {
		app.ConfigurePackageURLTTL(packageURLTTL)
	}
	app.ConfigureSearch(c.Search.LexicalDepth, c.Search.VectorDepth, c.Search.RerankDepth, c.Search.RRFK, c.Search.MaxLimit)
	app.ConfigureDefaultLimit(c.Search.DefaultLimit)
	app.Metrics().ActiveSkills.Store(uint64(len(docs)))
	app.Metrics().EmbeddingRequests.Store(embeddingRequests.Load())
	if c.Search.Reranker.BaseURL != "" {
		rerankConfig := rerank.Config{BaseURL: c.Search.Reranker.BaseURL, Model: c.Search.Reranker.Model, MaxCandidates: c.Search.Reranker.MaxCandidates}
		if c.Search.Reranker.Timeout != "" {
			if timeout, parseErr := time.ParseDuration(c.Search.Reranker.Timeout); parseErr == nil {
				rerankConfig.Timeout = timeout
			}
		}
		if c.Search.Reranker.APIKeyEnv != "" {
			rerankConfig.APIKey = os.Getenv(c.Search.Reranker.APIKeyEnv)
		}
		rerankerClient, rerankerErr := rerank.NewOpenAICompatible(rerankConfig)
		if rerankerErr != nil {
			slog.Error("reranker configuration failed", "error", rerankerErr)
			os.Exit(2)
		}
		app.ConfigureReranker(rerankerClient)
	}
	srv := &http.Server{Addr: c.Server.Listen, Handler: app.Handler(c.Server.MCPPath, c.Server.MaxRequestBodyBytes, auth)}
	go func() {
		slog.Info("skillet listening", "addr", c.Server.Listen, "mcp_path", c.Server.MCPPath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server stopped", "error", err)
			stop()
		}
	}()
	if len(c.Repositories) > 0 {
		pollState, stateErr := polling.NewSQLiteState(db)
		if stateErr != nil {
			slog.Error("polling state failed", "error", stateErr)
			os.Exit(2)
		}
		pollLeases, leaseErr := polling.NewSQLiteLeases(db)
		if leaseErr != nil {
			slog.Error("polling leases failed", "error", leaseErr)
			os.Exit(2)
		}
		var completed int32
		for _, configured := range c.Repositories {
			repo := configured
			go func() {
				mirror := gitstore.NewMirror(filepath.Join(c.Server.DataDir, "repositories", repo.ID+".git"))
				rules := make([]discovery.MetadataRule, 0, len(repo.SearchExclusions))
				for _, rule := range repo.SearchExclusions {
					rules = append(rules, discovery.MetadataRule{Key: rule.MetadataKey, Equals: rule.Equals})
				}
				syncRepo := catalogue.Repository{ID: repo.ID, OrganizationID: c.Organization.ID, URL: repo.URL, Ref: repo.Ref, TrustLevel: repo.TrustLevel, Owner: repo.Owner}
				coordinator := polling.Coordinator{
					State: pollState, Leases: pollLeases,
					Audit: func(auditCtx context.Context, event string, audited polling.Repository, details map[string]any) error {
						if details == nil {
							details = map[string]any{}
						}
						details["repository_id"] = audited.ID
						auditErr := catalog.RecordAudit(auditCtx, audited.OrganizationID, event, details)
						metrics := app.Metrics()
						switch event {
						case "repository_sync_started":
							metrics.RepositorySyncs.Add(1)
						case "repository_sync_failed":
							metrics.RepositorySyncFailures.Add(1)
						case "repository_sync_succeeded":
							if admitted, ok := details["admitted"].(int); ok {
								metrics.SkillRevisions.Add(uint64(admitted))
							}
							if quarantined, ok := details["quarantined"].(int); ok {
								metrics.SkillQuarantined.Add(uint64(quarantined))
							}
						}
						return auditErr
					},
					AuditError: func(_ context.Context, audited polling.Repository, event string, auditErr error) {
						app.Metrics().AuditFailures.Add(1)
						slog.Error("audit write failed", "event", event, "repository", audited.ID, "error", auditErr)
					},
					Observe: func(_ context.Context, _ polling.Repository, result polling.RunResult, duration time.Duration, syncErr error) {
						app.ObserveRepositorySync(duration, syncErr == nil && result.Outcome == polling.Synchronized)
					},
					Resolve: func(ctx context.Context, _ polling.Repository) (string, error) {
						if err := mirror.Init(ctx, repo.URL); err != nil {
							return "", err
						}
						return mirror.Fetch(ctx, repo.Ref)
					},
					Sync: func(ctx context.Context, _ polling.Repository, commit string) (polling.SyncResult, error) {
						result, err := ingest.SyncAtCommitWithOptions(ctx, mirror, syncRepo, packageStore, catalog, commit, ingest.Options{Include: repo.Include, Exclude: repo.Exclude, SearchExclusions: rules, PackageLimits: packagebuilder.Limits{MaxFiles: c.Packages.MaxFiles, MaxFileBytes: c.Packages.MaxFileBytes, MaxTotalBytes: c.Packages.MaxUncompressedBytes, MaxArchiveBytes: c.Packages.MaxArchiveBytes}})
						return polling.SyncResult{Commit: result.Commit, Admitted: result.Admitted, Quarantined: result.Quarantined}, err
					},
				}
				scheduler := polling.Scheduler{Jitter: 0.1, Sample: func() float64 { return float64(time.Now().UnixNano()%1000) / 1000.0 }}
				for {
					run, err := coordinator.RunOnce(ctx, polling.Repository{OrganizationID: c.Organization.ID, ID: repo.ID, URL: repo.URL, Ref: repo.Ref, PollInterval: repo.PollInterval})
					if err != nil {
						slog.Error("repository sync failed", "repository", repo.ID, "error", err)
					} else if run.Outcome == polling.Synchronized || run.Outcome == polling.SkippedUnchanged {
						if run.Outcome == polling.Synchronized {
							if refreshed, refreshErr := catalog.RoutingDocuments(ctx, c.Organization.ID, c.Search.SearchableMetadataKeys); refreshErr == nil {
								_ = index.Rebuild(refreshed)
								app.Metrics().ActiveSkills.Store(uint64(len(refreshed)))
							}
							slog.Info("repository sync succeeded", "repository", repo.ID, "commit", run.Commit, "admitted", run.Sync.Admitted, "quarantined", run.Sync.Quarantined)
						}
						if atomic.AddInt32(&completed, 1) == int32(len(c.Repositories)) {
							ready.InitialSync.Store(true)
						}
					}
					timer := time.NewTimer(scheduler.NextDelay(repo.PollInterval))
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
			}()
		}
	}
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

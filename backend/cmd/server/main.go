// Command server wires the feature packages together and starts the HTTP server.
// Wiring is manual constructor injection (ADR 0003); no business logic lives here.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexedwards/scs/mysqlstore"
	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql" // registers the "mysql" driver
	"github.com/jmoiron/sqlx"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/api"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/auth"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/event"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/idea"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/team"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/upload"
)

// apiBase must match `servers` in api/openapi.yaml.
const apiBase = "/api/v1"

func main() {
	if err := run(); err != nil {
		// Config.String redacts secrets, and errors here never embed the DSN.
		fmt.Fprintln(os.Stderr, "server:", err)
		os.Exit(1)
	}
}

// server implements api.ServerInterface by composing the feature handlers.
// Operations of features that have not landed yet are simply absent from the spec.
type server struct {
	*authHandler
	*teamHandler
	*ideaHandler
}

// Aliases give the embedded fields distinct names; both packages call their type Handler.
type (
	authHandler = auth.Handler
	teamHandler = team.Handler
	ideaHandler = idea.Handler
)

// ideaIdentity adapts auth's session user to idea's Actor: idea may not import auth
// (ADR 0003), so the translation lives here with the other wiring.
type ideaIdentity struct{ auth *auth.Service }

func (i ideaIdentity) CurrentUser(ctx context.Context) (idea.Actor, error) {
	u, err := i.auth.CurrentUser(ctx)
	if err != nil {
		return idea.Actor{}, err
	}
	return idea.Actor{EmployeeID: u.EmployeeID, Role: u.Role}, nil
}

func (server) GetHealth(c *gin.Context) { c.JSON(http.StatusOK, api.Health{Status: api.Ok}) }

func run() error {
	cfg, err := platform.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	log := platform.NewLogger(os.Stdout, cfg.LogLevel)
	slog.SetDefault(log)

	db, err := sqlx.Open("mysql", cfg.DB.DSN())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("ping database (%s): %w", cfg.DB, err)
	}

	clock := platform.SystemClock{}
	policy := event.NewPolicyFromConfig(cfg, clock)
	recorder := audit.NewRecorder(db, clock)

	// ADM-004: Admins exist only because the Vault-supplied file lists them. A conflict
	// with a Participant (or an unreadable file) must stop the server, not be skipped.
	if _, err := auth.SeedAdmins(context.Background(), db, recorder, cfg.AdminsFile, log); err != nil {
		return fmt.Errorf("seed admins: %w", err)
	}

	// Sessions live in MariaDB (ADR 0007). The store's cleanup goroutine is library housekeeping.
	store := mysqlstore.New(db.DB)
	defer store.StopCleanup()
	sessions := auth.NewSessionManager(store, cfg.SessionIdleTimeout, cfg.SessionLifetime)

	authSvc := auth.NewService(db, policy, auth.NewArgon2idHasher(), sessions, recorder, clock, log)
	teamSvc := team.NewService(db, authSvc, policy, recorder, log)

	// Idea files: scanned by ClamAV, stored in MinIO. Both must be reachable at start so a
	// misconfigured deploy fails now and not on the first upload (IDEA-005).
	objects, err := upload.NewMinIO(cfg.MinIO.Endpoint, cfg.MinIO.AccessKey, cfg.MinIO.SecretKey, cfg.MinIO.Bucket, cfg.MinIO.UseSSL)
	if err != nil {
		return fmt.Errorf("object store: %w", err)
	}
	depsCtx, cancelDeps := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelDeps()
	if err := objects.EnsureBucket(depsCtx); err != nil {
		return fmt.Errorf("object store (%s): %w", cfg.MinIO, err)
	}
	scanner := upload.ClamAV{Addr: cfg.ClamAVAddr}
	if err := scanner.Ping(depsCtx); err != nil {
		return fmt.Errorf("clamd (%s): %w", cfg.ClamAVAddr, err)
	}
	pipeline := &upload.Pipeline{Scanner: scanner, Store: objects, Log: log}
	ideaSvc := idea.NewService(db, ideaIdentity{auth: authSvc}, teamSvc, policy, pipeline, recorder, clock, log)

	srv := server{
		authHandler: auth.NewHandler(authSvc, log),
		teamHandler: team.NewHandler(teamSvc, log),
		ideaHandler: idea.NewHandler(ideaSvc, log),
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	// X-Forwarded-For is client-controlled: trust only the proxies in TRUSTED_PROXIES
	// (none by default), otherwise audit source addresses and the AUTH-004 rate-limit
	// key could be forged (ADR 0017).
	if err := router.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		return fmt.Errorf("trusted proxies: %w", err)
	}
	// Registered before the routes so it also covers the generated handlers.
	router.Use(platform.RateLimitByIP(cfg.AuthRateLimit, clock, log, apiBase+"/auth/"))
	// AUTH-009: needs the session loaded, which sessions.LoadAndSave below guarantees.
	router.Use(platform.CSRF(sessions, log))
	// AUTH-008: a flagged session can only change its password, read /auth/me or log out.
	router.Use(auth.RequirePasswordChange(sessions))
	api.RegisterHandlersWithOptions(router, srv, api.GinServerOptions{BaseURL: apiBase})

	httpSrv := &http.Server{
		Addr: cfg.HTTPAddr,
		// scs wraps the whole router so every handler sees the loaded session.
		Handler:           sessions.LoadAndSave(router),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.HTTPAddr, "eventYear", cfg.EventYear)
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

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
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
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
	*auth.Handler
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

	// Sessions live in MariaDB (ADR 0007). The store's cleanup goroutine is library housekeeping.
	store := mysqlstore.New(db.DB)
	defer store.StopCleanup()
	sessions := auth.NewSessionManager(store, cfg.SessionIdleTimeout, cfg.SessionLifetime)

	authSvc := auth.NewService(db, policy, auth.NewArgon2idHasher(), sessions, recorder, clock, log)
	srv := server{Handler: auth.NewHandler(authSvc, log)}

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

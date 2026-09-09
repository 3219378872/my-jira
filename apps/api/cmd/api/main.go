package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"my-jira/apps/api/internal/application"
	"my-jira/apps/api/internal/foundation"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := database.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	deps := platform.Dependencies{DB: db, Policy: &identity.SQLPolicy{DB: db.SQL}, Jobs: jobs.Outbox{}}
	origins := os.Getenv("ALLOWED_ORIGINS")
	if origins == "" {
		origins = os.Getenv("APP_ORIGIN")
	}
	if origins == "" {
		origins = "http://127.0.0.1:4173,http://localhost:4173"
	}
	gin.SetMode(gin.ReleaseMode)
	router := application.Router(deps, foundation.Config{AppOrigin: origins, CookieSecure: os.Getenv("COOKIE_SECURE") == "true"})
	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8088"
	}
	server := &http.Server{Addr: addr, Handler: router, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()
	slog.Info("API listening", "address", addr)
	if err = server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"my-jira/apps/api/internal/automation"
	"my-jira/apps/api/internal/files"
	"my-jira/apps/api/internal/foundation"
	"my-jira/apps/api/internal/integrations"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/quality"
	"my-jira/apps/api/internal/support"
	"my-jira/apps/api/internal/workitems"
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
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:26379"
	}
	option := asynq.RedisClientOpt{Addr: addr, Password: os.Getenv("REDIS_PASSWORD")}
	client := asynq.NewClient(option)
	defer client.Close()
	go jobs.RunDispatcher(ctx, db, client)
	periodic := asynq.NewScheduler(option, nil)
	if _, err = periodic.Register("@every 15m", asynq.NewTask("quality.reconcile", []byte("{}")), asynq.Unique(14*time.Minute)); err != nil {
		slog.Error("quality reconciliation registration failed", "error", err)
		os.Exit(1)
	}
	if err = periodic.Start(); err != nil {
		slog.Error("periodic task scheduler failed", "error", err)
		os.Exit(1)
	}
	defer periodic.Shutdown()
	server := asynq.NewServer(option, asynq.Config{Concurrency: 8})
	mux := asynq.NewServeMux()
	foundation.RegisterJobs(mux, deps)
	integrations.RegisterJobs(mux, deps)
	workitems.RegisterJobs(mux, deps)
	support.RegisterJobs(mux, deps)
	automation.RegisterTasks(mux, deps)
	quality.RegisterTasks(mux, deps)
	if err = server.Start(mux); err != nil {
		slog.Error("worker start failed", "error", err)
		os.Exit(1)
	}
	go workitems.RunScheduler(ctx, deps)
	go automation.RunScheduler(ctx, deps)
	go cleanupAssets(ctx, deps)
	<-ctx.Done()
	server.Shutdown()
}

func cleanupAssets(ctx context.Context, deps platform.Dependencies) {
	// The first cleanup is deliberately delayed: worker startup itself must
	// not immediately remove assets from the configured object store.
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanupContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
			if err := files.Cleanup(cleanupContext, deps); err != nil && ctx.Err() == nil {
				slog.Error("expired asset cleanup failed", "error", err)
			}
			cancel()
		}
	}
}

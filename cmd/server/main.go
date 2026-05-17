// Command server is the single, self-contained CuriosityEngine service.
//
// It runs on Cloud Run with min-instances=0: it is fully idle (and free) until
// a request arrives. The same binary serves three surfaces from one process:
//
//	POST /interactions  Discord HTTP-interaction webhook (Ed25519 verified)
//	POST /cron/daily    once-a-day self-update, invoked by Cloud Scheduler (OIDC verified)
//	GET  /              public, server-rendered leaderboard
//	GET  /health        liveness probe
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Embed the IANA timezone database into the binary so the distroless
	// runtime image (which ships no tzdata) can still resolve Asia/Kolkata.
	_ "time/tzdata"

	"github.com/dmjone/curiosity-engine/internal/config"
	"github.com/dmjone/curiosity-engine/internal/secrets"
	"github.com/dmjone/curiosity-engine/internal/server"
	"github.com/dmjone/curiosity-engine/internal/store"
	"github.com/dmjone/curiosity-engine/internal/vertex"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	ctx := context.Background()
	cfg := config.Load(ctx)
	slog.Info("starting curiosity-engine",
		"project", cfg.ProjectID, "location", cfg.Location, "model", cfg.GeminiModel)

	st, err := store.New(ctx, cfg.ProjectID, cfg.FirestoreDB)
	if err != nil {
		slog.Error("init firestore", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	vx, err := vertex.New(ctx, cfg.ProjectID, cfg.Location, cfg.GeminiModel)
	if err != nil {
		slog.Error("init vertex", "err", err)
		os.Exit(1)
	}

	sm, err := secrets.New(ctx, cfg.ProjectID)
	if err != nil {
		slog.Error("init secret manager", "err", err)
		os.Exit(1)
	}

	srv := server.New(cfg, st, vx, sm)

	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("listening", "port", cfg.Port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	slog.Info("shutdown complete")
}

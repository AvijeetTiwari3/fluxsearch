// FluxSearch Engine — Entry point
// Binds to 0.0.0.0:8080 (primary) and 0.0.0.0:10000 (Render port) simultaneously.
package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/sde/fluxsearch/internal/api"
	"github.com/sde/fluxsearch/internal/storage"
)

func main() {
	// ── Logger ──────────────────────────────────────────────────────────────
	log, _ := zap.NewProduction()
	defer log.Sync() //nolint:errcheck

	// ── Config from environment ──────────────────────────────────────────────
	dbDSN := os.Getenv("DATABASE_URL")
	if dbDSN == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	vecDim := 384 // default: all-MiniLM-L6-v2 embedding dimension

	// ── PostgreSQL ───────────────────────────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	db, err := storage.New(ctx, dbDSN)
	cancel()
	if err != nil {
		log.Fatal("failed to connect to postgres", zap.Error(err))
	}
	defer db.Close()
	log.Info("postgres connected")

	// ── API Handler ──────────────────────────────────────────────────────────
	handler := api.NewHandler(api.Config{
		Log:    log,
		DB:     db,
		VecDim: vecDim,
	})

	router := handler.Router()

	// ── HTTP Servers (dual port binding for Render) ──────────────────────────
	servers := []*http.Server{
		{
			Addr:         "0.0.0.0:8080",
			Handler:      router,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 30 * time.Second,
			IdleTimeout:  120 * time.Second,
		},
		{
			Addr:         "0.0.0.0:10000",
			Handler:      router,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 30 * time.Second,
			IdleTimeout:  120 * time.Second,
		},
	}

	for _, srv := range servers {
		srv := srv
		ln, err := net.Listen("tcp", srv.Addr)
		if err != nil {
			log.Fatal("listen failed", zap.String("addr", srv.Addr), zap.Error(err))
		}
		go func() {
			log.Info("FluxSearch engine listening", zap.String("addr", srv.Addr))
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				log.Error("server error", zap.Error(err))
			}
		}()
	}

	// ── Graceful Shutdown ────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutdown signal received")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("server shutdown error", zap.Error(err))
		}
	}
	log.Info("FluxSearch engine stopped")
}

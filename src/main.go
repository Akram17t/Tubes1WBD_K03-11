package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/platformdb"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	config, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	platformDB, err := platformdb.Connect(ctx, config.PlatformDatabaseURL)
	if err != nil {
		return err
	}
	defer platformDB.Close()

	if err := auth.EnsureAdmin(ctx, platformDB, config.AdminUsername, config.AdminPassword); err != nil {
		return fmt.Errorf("seed platform admin: %w", err)
	}

	router, err := newRouter(Dependencies{Config: config, PlatformDB: platformDB})
	if err != nil {
		return fmt.Errorf("build router: %w", err)
	}
	server := &http.Server{
		Addr:              config.AppAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("HTTP server listening on %s", config.AppAddr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
		return nil
	}
}

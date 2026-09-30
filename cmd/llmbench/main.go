// Command llmbench is the LLMBench server: an LLM benchmark and performance
// testing tool with an embedded web UI.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/metalgeekhub/llmbench/internal/api"
	"github.com/metalgeekhub/llmbench/internal/chat"
	"github.com/metalgeekhub/llmbench/internal/config"
	"github.com/metalgeekhub/llmbench/internal/definitions"
	"github.com/metalgeekhub/llmbench/internal/profiles"
	"github.com/metalgeekhub/llmbench/internal/runner"
	"github.com/metalgeekhub/llmbench/internal/secrets"
	"github.com/metalgeekhub/llmbench/internal/sources"
	"github.com/metalgeekhub/llmbench/internal/store"
	"github.com/metalgeekhub/llmbench/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `LLMBench - LLM benchmark and performance testing

Usage:
  llmbench [command]

Commands:
  serve     Start the web UI and API server (default)
  version   Print the version
  help      Show this help

Configuration is read from LLMB_* environment variables and the optional
YAML file named by LLMB_CONFIG_FILE. See docs/PLAN.md.
`

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		if err := serve(); err != nil {
			slog.Error("fatal", "err", err)
			os.Exit(1)
		}
	case "version", "--version", "-v":
		fmt.Println("llmbench", version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	case "agent":
		fmt.Fprintln(os.Stderr, "agent mode is not implemented yet")
		os.Exit(2)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

func serve() error {
	if os.Getenv("LLMB_DEBUG") != "" {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("creating data dir: %w", err)
	}
	if cfg.AdminPassword != "" {
		slog.Warn("LLMB_ADMIN_PASSWORD is set but authentication is not implemented yet; the UI is unprotected")
	}

	box, generated, err := secrets.Load(cfg.SecretKey, cfg.DataDir)
	if err != nil {
		return fmt.Errorf("secret key: %w", err)
	}
	if generated {
		slog.Warn("LLMB_SECRET_KEY not set; generated a random key",
			"file", filepath.Join(cfg.DataDir, "secret.key"))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbPath := filepath.Join(cfg.DataDir, "llmbench.db")
	st, err := store.OpenSQLite(ctx, dbPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			slog.Error("closing database", "err", err)
		}
	}()

	srcs := sources.New(st, box, cfg.Sources)
	for _, s := range cfg.Sources {
		slog.Info("environment source", "name", s.Name, "type", s.Type, "base_url", s.BaseURL)
	}

	if n, err := st.MarkInterruptedRuns(ctx); err != nil {
		return err
	} else if n > 0 {
		slog.Warn("marked benchmark runs from a previous process as interrupted", "count", n)
	}
	runs := runner.New(st, srcs)
	// Runs after the HTTP server stops and before the database closes, so
	// stopped runs record their partial results.
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		runs.Shutdown(shutdownCtx)
	}()

	files, built := ui.Files()
	if !built {
		slog.Warn("web UI not built into this binary; serving a placeholder page (run make build)")
	}

	handler := api.New(api.Deps{
		Version:     version,
		Store:       st,
		Sources:     srcs,
		Chat:        chat.NewService(st, srcs),
		Profiles:    profiles.NewService(st, srcs),
		Definitions: definitions.NewService(st, runs),
		Runner:      runs,
		UI:          files,
	})

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: chat responses stream for as long as the model generates.
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("llmbench listening", "addr", cfg.Listen, "data_dir", cfg.DataDir, "version", version)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	return nil
}

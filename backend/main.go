package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	address := envOr("ADDRESS", "127.0.0.1:8080")
	databasePath := envOr("DATABASE_PATH", "data/386gpt.db")
	hermesPath, err := defaultHermesConfigPath()
	if err != nil {
		slog.Error("resolve Hermes config", "error", err)
		os.Exit(1)
	}
	hermesPath = envOr("HERMES_CONFIG", hermesPath)
	llm, err := loadHermesAgent(hermesPath)
	if err != nil {
		slog.Error("load Hermes settings", "error", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		slog.Error("create data directory", "error", err)
		os.Exit(1)
	}
	store, err := openStore(databasePath)
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	provision, err := accountProvisioner(hermesPath)
	if err != nil {
		slog.Error("configure account environments", "error", err)
		os.Exit(1)
	}
	app, err := newAccountManager(store, llm, filepath.Join(filepath.Dir(databasePath), "accounts"), envOr("ACCESS_OWNER_EMAIL", "mauriciootta@gmail.com"), provision)
	if err != nil {
		slog.Error("configure accounts", "error", err)
		os.Exit(1)
	}
	defer app.close()
	var handler http.Handler
	if googleFile := os.Getenv("GOOGLE_OAUTH_CLIENT_FILE"); googleFile != "" {
		auth, authErr := newGoogleAuth(store.db, googleFile, envOr("PUBLIC_ORIGIN", "https://386gpt.truvis.co"))
		err = authErr
		if err == nil {
			handler = auth.protect(cors(app))
		}
	} else {
		handler, err = authentication(address, cors(app))
	}
	if err != nil {
		slog.Error("configure authentication", "error", err)
		os.Exit(1)
	}
	app.recover()
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		slog.Info("386GPT backend online", "address", address, "database", databasePath, "gateway", llm.baseURL)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("serve", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		slog.Error("shutdown", "error", err)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

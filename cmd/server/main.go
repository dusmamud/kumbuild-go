package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"kumbuild-go/internal/config"
	"kumbuild-go/internal/email"
	"kumbuild-go/internal/handlers"
	"kumbuild-go/internal/store"
	"kumbuild-go/internal/turnstile"
)

func main() {
	log.Println("Starting Kumbuild Orders Backend...")
	cfg := config.Load()

	if err := cfg.Validate(); err != nil {
		log.Fatalf("[FATAL] %v", err)
	}

	var s store.Store
	if strings.ToLower(strings.TrimSpace(cfg.DatabaseURL)) == "memory" {
		log.Println("[INFO] Store: in-memory (dev only)")
		s = store.NewMemoryStore()
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		pgStore, err := store.NewPostgresStore(ctx, cfg.DatabaseURL)
		if err != nil {
			log.Fatalf("[FATAL] Failed to connect to PostgreSQL: %v", err)
		}
		log.Println("[INFO] Store: PostgreSQL")
		s = pgStore
	}
	defer s.Close()

	// Initialize email sender & turnstile verifier
	emailSender := email.NewBrevoSender(cfg.BrevoAPIKey, cfg.MailFrom)
	turnstileVerifier := turnstile.NewVerifier(cfg.TurnstileSecretKey)

	// Build HTTP router
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)

	h := handlers.NewHandler(cfg, s, emailSender, turnstileVerifier)
	handlers.RegisterRoutes(r, h)

	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown channel
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("[INFO] Kumbuild orders server listening on http://localhost%s", addr)
		serverErrors <- srv.ListenAndServe()
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] Server error: %v", err)
		}
	case sig := <-shutdown:
		log.Printf("[INFO] Received signal %v; initiating graceful shutdown", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("[ERROR] Graceful shutdown failed: %v", err)
			_ = srv.Close()
		}
		log.Println("[INFO] Server stopped cleanly")
	}
}

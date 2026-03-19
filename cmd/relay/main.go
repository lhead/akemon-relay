package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/akemon/akemon-relay/internal/config"
	"github.com/akemon/akemon-relay/internal/server"
	"github.com/akemon/akemon-relay/internal/store"
)

func main() {
	cfg := config.Default()

	flag.StringVar(&cfg.Addr, "addr", cfg.Addr, "listen address")
	flag.StringVar(&cfg.DBPath, "db", cfg.DBPath, "SQLite database path")
	flag.Parse()

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		log.Fatalf("failed to migrate database: %v", err)
	}

	srv := server.New(cfg, db)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("akemon-relay starting on %s", cfg.Addr)
	if err := srv.Run(ctx); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

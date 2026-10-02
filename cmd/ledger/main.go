package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/audemed44/ledger/internal/ledger"
	"github.com/audemed44/ledger/web"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "check-statement" {
		flags := flag.NewFlagSet("check-statement", flag.ExitOnError)
		path := flags.String("pdf", "", "path to local statement PDF")
		flags.Parse(os.Args[2:])
		text, err := ledger.ExtractPDF(context.Background(), *path, os.Getenv("LEDGER_PDF_PASSWORD"))
		if err != nil {
			log.Fatal(err)
		}
		statement, err := ledger.ParseHDFCStatement(text)
		if err != nil {
			log.Fatal(err)
		}
		debit, credit := 0, 0
		for _, t := range statement.Transactions {
			if t.Direction == "debit" {
				debit++
			} else {
				credit++
			}
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"issuer": statement.Issuer, "transactions": len(statement.Transactions), "debits": debit, "credits": credit, "row_totals_match": true, "balanced": statement.Balanced, "discrepancy_minor_units": statement.Discrepancy, "warnings": statement.Warnings})
		if !statement.Balanced {
			fmt.Fprintln(os.Stderr, "Statement flagged; nothing imported")
			os.Exit(2)
		}
		return
	}

	demo := flag.Bool("demo", false, "seed synthetic examples; disables Gmail")
	flag.Parse()
	token := os.Getenv("LEDGER_TOKEN")
	if len(token) < 24 {
		log.Fatal("LEDGER_TOKEN must contain at least 24 characters")
	}
	interval, err := time.ParseDuration(env("LEDGER_POLL_INTERVAL", "15m"))
	if err != nil || interval < time.Minute {
		log.Fatal("LEDGER_POLL_INTERVAL must be at least 1m")
	}
	store, err := ledger.Open(env("LEDGER_DATA_DIR", "/data"))
	if err != nil {
		log.Fatal("Could not open Ledger data directory")
	}
	defer store.DB.Close()
	cfg := ledger.MailConfig{User: os.Getenv("GMAIL_USER"), Password: os.Getenv("GMAIL_APP_PASSWORD"), Label: env("GMAIL_LABEL", "Bank"), Interval: interval, Backfill: env("LEDGER_BACKFILL", "false") == "true"}
	if *demo {
		cfg.User = ""
		cfg.Password = ""
		if err = store.SeedDemo(); err != nil {
			log.Fatal("Could not seed demo")
		}
	}
	poller := &ledger.Poller{Store: store, Config: cfg}
	assets, err := fs.Sub(web.Files, "dist")
	if err != nil {
		log.Fatal(err)
	}
	app := &ledger.Server{Store: store, Poller: poller, Token: token, SecureCookies: env("LEDGER_SECURE_COOKIES", "true") == "true", Demo: *demo, Files: assets}
	server := &http.Server{Addr: env("LEDGER_LISTEN", ":8080"), Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 11 * time.Minute, IdleTimeout: 60 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); poller.Run(ctx) }()
	go func() {
		<-ctx.Done()
		shutdown, c := context.WithTimeout(context.Background(), 15*time.Second)
		defer c()
		server.Shutdown(shutdown)
	}()
	log.Printf("Ledger listening on %s", server.Addr)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	cancel()
	<-done
}

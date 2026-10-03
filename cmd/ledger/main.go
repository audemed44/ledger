// Command ledger fills a private finance ledger from bank and card emails in
// one Gmail label, from one small binary.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // the runtime image may have no zoneinfo; TZ needs this

	"github.com/audemed44/ledger/internal/gmail"
	"github.com/audemed44/ledger/internal/server"
	"github.com/audemed44/ledger/internal/smartstatement"
	"github.com/audemed44/ledger/internal/statements"
	"github.com/audemed44/ledger/internal/store"
	"github.com/audemed44/ledger/web"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			os.Exit(healthcheck())
		case "check-statement":
			os.Exit(checkStatement(os.Args[2:]))
		}
	}
	demo := flag.Bool("demo", false, "seed synthetic examples; disables Gmail")
	flag.Parse()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	token := os.Getenv("LEDGER_TOKEN")
	if token == "" {
		slog.Error("set LEDGER_TOKEN: it's what you sign in with, and what Foyer uses for the widget")
		os.Exit(1)
	}
	if len(token) < 16 {
		slog.Warn("LEDGER_TOKEN is short; generate a new one with: openssl rand -hex 32")
	}
	interval, err := time.ParseDuration(env("LEDGER_POLL_INTERVAL", "15m"))
	if err != nil || interval < time.Minute {
		slog.Error("LEDGER_POLL_INTERVAL must be a duration of at least 1m")
		os.Exit(1)
	}
	db, err := store.Open(env("LEDGER_DATA_DIR", "/data"))
	if err != nil {
		slog.Error("could not open the data folder", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	db.PDFPasswords = strings.Split(os.Getenv("LEDGER_PDF_PASSWORDS"), "|")
	if env("LEDGER_FETCH_STATEMENT_LINKS", "true") == "true" && !*demo {
		db.Statements = smartstatement.New()
	}

	cfg := gmail.Config{
		User:     os.Getenv("GMAIL_USER"),
		Password: os.Getenv("GMAIL_APP_PASSWORD"),
		Label:    env("GMAIL_LABEL", "Bank"),
		Interval: interval,
		Backfill: env("LEDGER_BACKFILL", "false") == "true",
	}
	if *demo {
		cfg.User, cfg.Password = "", ""
		if err = db.SeedDemo(); err != nil {
			slog.Error("could not seed the demo", "err", err)
			os.Exit(1)
		}
	}
	poller := &gmail.Poller{Store: db, Config: cfg}
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(err)
	}
	app := &server.Server{
		Store:         db,
		Poller:        poller,
		Token:         token,
		SecureCookies: env("LEDGER_SECURE_COOKIES", "true") == "true",
		Demo:          *demo,
		Files:         dist,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	polling := make(chan struct{})
	go func() {
		defer close(polling)
		poller.Run(ctx)
	}()

	srv := &http.Server{
		Addr:              env("LEDGER_LISTEN", ":8080"),
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      11 * time.Minute, // a manual sync can take up to 10
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	slog.Info("ledger listening", "addr", srv.Addr, "gmail", cfg.User != "" && cfg.Password != "", "demo", *demo)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
	stop()
	<-polling
}

// layoutIDs lists the statement layouts check-statement can use.
func layoutIDs() string {
	ids := []string{}
	for _, a := range statements.Adapters {
		ids = append(ids, a.ID)
	}
	return strings.Join(ids, ", ")
}

// healthcheck is the container's HEALTHCHECK.
func healthcheck() int {
	client := http.Client{Timeout: 3 * time.Second}
	port := env("LEDGER_LISTEN", ":8080")
	port = port[strings.LastIndex(port, ":")+1:]
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// checkStatement validates a local PDF statement without importing it. The
// password comes from LEDGER_PDF_PASSWORD. Output holds counts and
// validation results only: no merchants, account numbers or amounts. It
// exits 0 when the statement validated and 2 when it needs review.
func checkStatement(args []string) int {
	flags := flag.NewFlagSet("check-statement", flag.ExitOnError)
	path := flags.String("pdf", "", "path to local statement PDF")
	layout := flags.String("layout", "hdfc-credit-card", "statement layout ID: "+layoutIDs())
	flags.Parse(args)
	text, err := statements.Extract(context.Background(), *path, os.Getenv("LEDGER_PDF_PASSWORD"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	statement, err := statements.ParseWith(*layout, text)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	debits, credits := 0, 0
	for _, t := range statement.Transactions {
		if t.Direction == "debit" {
			debits++
		} else {
			credits++
		}
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"issuer":                  statement.Issuer,
		"transactions":            len(statement.Transactions),
		"debits":                  debits,
		"credits":                 credits,
		"row_totals_match":        true,
		"balanced":                statement.Balanced,
		"discrepancy_minor_units": statement.Discrepancy,
		"warnings":                statement.Warnings,
	})
	if !statement.Balanced {
		fmt.Fprintln(os.Stderr, "Statement flagged; nothing imported")
		return 2
	}
	return 0
}

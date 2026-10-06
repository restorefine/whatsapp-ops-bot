// Command server runs the WhatsApp ops bot.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // embed time zone data so TZ works in any base image

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/commands"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/config"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/whatsapp"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "check /healthz on the local server and exit (for Docker HEALTHCHECK)")
	flag.Parse()
	if *healthcheck {
		os.Exit(checkHealth())
	}

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	if err := run(cfg, log); err != nil {
		log.Error("server stopped with error", "err", err)
		os.Exit(1)
	}
}

func run(cfg *config.Config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var messenger whatsapp.Messenger
	if cfg.WADryRun {
		log.Warn("WA_DRY_RUN is on: replies are logged, not sent")
		messenger = whatsapp.DryRunMessenger{Log: log}
	} else {
		messenger = whatsapp.NewCloudClient(cfg.WAAPIBaseURL, cfg.WAGraphVersion, cfg.WAPhoneNumberID, cfg.WAToken, log)
	}

	contacts := whatsapp.NewContacts()
	var team []commands.Contact
	var teamNumbers []string
	for _, c := range cfg.Team {
		team = append(team, commands.Contact{Name: c.Name, Number: c.Number})
		teamNumbers = append(teamNumbers, c.Number)
	}

	router := &commands.Router{
		ClickUp:   clickup.NewHTTPClient(cfg.ClickUpBaseURL, cfg.ClickUpToken, cfg.ClickUpTeamID, log),
		Messenger: messenger,
		Uploads:   cfg.UploadsFolder,
		Team:      team,
		Contacts:  contacts,
		Loc:       cfg.Location,
		Now:       time.Now,
		Log:       log,
	}

	webhook := whatsapp.NewWebhook(whatsapp.WebhookConfig{
		VerifyToken:   cfg.WAVerifyToken,
		AppSecret:     cfg.WAAppSecret,
		OwnerNumbers:  cfg.OwnerNumbers,
		TeamNumbers:   teamNumbers,
		Contacts:      contacts,
		PhoneNumberID: cfg.WAPhoneNumberID,
	}, router, log)
	// Workers get their own context so in-flight replies can finish during shutdown.
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	webhook.Start(workerCtx)

	if cfg.ReminderOn {
		reminder := &commands.Reminder{
			Router:   router,
			Owners:   cfg.OwnerNumbers,
			Hour:     cfg.ReminderHour,
			Minute:   cfg.ReminderMinute,
			Template: cfg.ReminderTemplate,
			Lang:     cfg.ReminderLang,
		}
		go reminder.Run(ctx)
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           routes(webhook),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("server listening", "addr", cfg.HTTPAddr, "tz", cfg.Location.String(), "graph_version", cfg.WAGraphVersion)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown", "err", err)
	}

	done := make(chan struct{})
	go func() { webhook.Stop(); close(done) }()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		cancelWorkers()
		log.Warn("gave up waiting for in-flight messages")
	}
	log.Info("stopped")
	return nil
}

func routes(webhook *whatsapp.Webhook) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ok")
	})
	r.Get("/webhook", webhook.Verify)
	r.Post("/webhook", webhook.Receive)
	return r
}

func checkHealth() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthz returned", resp.Status)
		return 1
	}
	return 0
}

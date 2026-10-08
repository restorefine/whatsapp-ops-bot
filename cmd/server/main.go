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
	"github.com/prabishdangi/whatsapp-ops-bot/internal/metricool"
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

	commands.SetClock(commands.Clock{Label: cfg.TZLabel, Second: cfg.SecondTZ, SecondLabel: cfg.SecondLabel})
	contacts := whatsapp.NewContacts()
	var numbers []string
	people := func(ps []config.Person) []commands.Contact {
		var out []commands.Contact
		for _, p := range ps {
			out = append(out, commands.Contact{Name: p.Name, Number: p.Number})
			numbers = append(numbers, p.Number)
		}
		return out
	}
	admins, team := people(cfg.Admins), people(cfg.Team)

	clickupClient := clickup.NewHTTPClient(cfg.ClickUpBaseURL, cfg.ClickUpToken, cfg.ClickUpTeamID, log)
	clickupClient.Dates = clickup.Dates{Loc: cfg.Location, SetIn: []*time.Location{cfg.Location}}
	if cfg.SecondTZ != nil {
		clickupClient.Dates.SetIn = append(clickupClient.Dates.SetIn, cfg.SecondTZ)
	}

	router := &commands.Router{
		ClickUp:   clickupClient,
		Messenger: messenger,
		Uploads:   cfg.UploadsFolder,
		Admins:    admins,
		Team:      team,
		Contacts:  contacts,
		Loc:       cfg.Location,
		Now:       time.Now,
		Log:       log,
	}

	if cfg.MetricoolToken != "" {
		router.Metricool = metricool.New(cfg.MetricoolBaseURL, cfg.MetricoolToken, cfg.MetricoolUserID, log)
	}
	router.BrandMap = commands.BrandMap(cfg.MetricoolBrands)
	router.Manual = cfg.UploadsManual
	for list, at := range cfg.PostingDeadlines {
		if list == "*" {
			router.Deadlines.Default, router.Deadlines.HasDefault = at, true
			continue
		}
		if router.Deadlines.ByBrand == nil {
			router.Deadlines.ByBrand = map[string][2]int{}
		}
		router.Deadlines.ByBrand[commands.BrandKey(list)] = at
	}

	webhook := whatsapp.NewWebhook(whatsapp.WebhookConfig{
		VerifyToken:   cfg.WAVerifyToken,
		AppSecret:     cfg.WAAppSecret,
		Numbers:       numbers,
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
			Owners:   cfg.AdminNumbers(),
			Hour:     cfg.ReminderHour,
			Minute:   cfg.ReminderMinute,
			Template: cfg.ReminderTemplate,
			Lang:     cfg.ReminderLang,
		}
		go reminder.Run(ctx)
	}

	if events := commands.PostingSchedule(cfg.PostingTimes, router.Deadlines); len(events) > 0 {
		posting := &commands.PostingReminder{Router: router, Admins: cfg.AdminNumbers(), Events: events}
		go posting.Run(ctx)
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

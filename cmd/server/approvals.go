package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/approvals"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/commands"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/config"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/drive"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/whatsapp"
)

func configureApprovals(ctx context.Context, cfg *config.Config, router *commands.Router, messenger whatsapp.Messenger, contacts *whatsapp.Contacts, log *slog.Logger) (http.Handler, func(string) bool, error) {
	if !cfg.ApprovalsEnabled {
		return nil, nil, nil
	}
	unlock, err := approvals.Lock(filepath.Join(filepath.Dir(cfg.ApprovalsKeyFile), "portal.lock"))
	if err != nil {
		return nil, nil, err
	}
	ready := false
	defer func() {
		if !ready {
			unlock()
		}
	}()
	key, err := os.ReadFile(cfg.ApprovalsKeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("read portal signing key: %w", err)
	}
	setupCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	// OAuth refreshing needs the service lifetime, not the startup timeout.
	d, err := drive.Open(ctx, cfg.ApprovalsCredentials, cfg.ApprovalsToken)
	if err != nil {
		return nil, nil, err
	}
	service, err := approvals.New(setupCtx, d, cfg.Location)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range []string{"suranjana", "rohit", "harpreet"} {
		for _, person := range cfg.Admins {
			if strings.HasPrefix(person.Name, name) {
				service.Staff = append(service.Staff, person.Number)
				if name == "suranjana" {
					service.Manager = person.Number
				}
				break
			}
		}
	}
	if cfg.ApprovalsNotify && (len(service.Staff) != 3 || service.Manager == "") {
		return nil, nil, fmt.Errorf("portal alerts require named admin mappings for Suranjana, Rohit and Harpreet")
	}
	web, err := approvals.NewWeb(service, key, cfg.ApprovalsOrigin, cfg.ApprovalsStaticDir)
	if err != nil {
		return nil, nil, err
	}
	portalMessenger := messenger
	if cloud, ok := messenger.(*whatsapp.CloudClient); ok {
		copy := *cloud
		copy.MaxAttempts = 1
		portalMessenger = &copy
	}
	delivery := approvals.Delivery{Enabled: cfg.ApprovalsNotify, Messenger: portalMessenger, Contacts: contacts, NewTemplate: cfg.ApprovalsNewTemplate, ReminderTemplate: cfg.ApprovalsReminderTemplate, InternalTemplate: cfg.ApprovalsInternalTemplate, Language: cfg.ApprovalsLanguage, Link: web.Link, Deadlines: cfg.PostingDeadlines}
	router.VideoRemind = func(ctx context.Context, name string) string { return service.Remind(ctx, name, delivery) }
	handler, editorRun := withEditorCounts(cfg, router, service, web, key, log)
	go func() {
		defer unlock()
		var wg sync.WaitGroup
		if editorRun != nil {
			wg.Add(1)
			go func() { defer wg.Done(); editorRun(ctx) }()
		}
		service.Run(ctx, delivery, log)
		wg.Wait()
	}()
	ready = true
	log.Info("video approvals enabled", "notifications_enabled", cfg.ApprovalsNotify)
	return handler, service.KnownNumber, nil
}

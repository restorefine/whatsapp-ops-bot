package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/approvals"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/commands"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/config"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/editorcounts"
	"github.com/prabishdangi/whatsapp-ops-bot/internal/whatsapp"
)

func withEditorCounts(cfg *config.Config, router *commands.Router, store *approvals.Service, portal http.Handler, key []byte, log *slog.Logger) (http.Handler, func(context.Context)) {
	if len(cfg.VideoTeam) == 0 {
		return portal, nil
	}
	editors := []editorcounts.Editor{}
	for _, p := range cfg.VideoTeam {
		name := []rune(p.Name)
		name[0] = unicode.ToUpper(name[0])
		editors = append(editors, editorcounts.Editor{Key: p.Name, Name: string(name), Number: p.Number})
	}
	service := &editorcounts.Service{Source: router.ClickUp, Store: store, Editors: editors, Admins: cfg.AdminNumbers(), Now: time.Now}
	web, err := editorcounts.NewWeb(service, key, cfg.ApprovalsOrigin, cfg.ApprovalsStaticDir, cfg.AdminNumbers())
	if err != nil {
		log.Error("editor report unavailable", "err", err)
		return portal, nil
	}
	router.EditorCounts = func(ctx context.Context, number, args string) string {
		filter := strings.ToLower(strings.TrimSpace(args))
		if filter != "" {
			found := false
			for _, e := range editors {
				found = found || e.Key == filter
			}
			if !found {
				return "Use /editors for all video editors, or /editors <name> using a name in VIDEO_TEAM_WA_NUMBERS."
			}
		}
		report, err := service.Report(ctx)
		if err != nil {
			log.Error("editor count failed", "err", err)
			return "Couldn't verify the full editor count from ClickUp and Drive. No incomplete total was used. Try again shortly."
		}
		link, err := web.Link(number)
		if err != nil {
			return "Couldn't create your private admin report link. Try again."
		}
		var b strings.Builder
		b.WriteString("*Editor video counts*\n_20 videos per batch · no monthly reset_\n")
		for _, e := range report.Editors {
			if (filter != "" && e.Key != filter) || e.Inactive {
				continue
			}
			fmt.Fprintf(&b, "\n*%s*\nBatch %d: %d/20 · %d to go\n%d full batches · %d recorded videos\n", e.Name, e.CurrentBatch, e.CurrentCount, e.Remaining, e.FullBatches, e.Total)
			if e.ReviewCount > 0 || len(e.Warnings) > 0 {
				fmt.Fprintf(&b, "Evidence to review: %d recorded task(s), %d excluded task(s)\n", e.ReviewCount, len(e.Warnings))
			}
		}
		b.WriteString("\n*Live report + Excel download*\n" + link + "\n\n_Admin link: open within 15 minutes; single use. Older work is historical evidence, outside the new cycles. Full batches are ready for payment review; no payment is recorded._")
		return b.String()
	}
	messenger := router.Messenger
	if cloud, ok := messenger.(*whatsapp.CloudClient); ok {
		copy := *cloud
		copy.MaxAttempts = 1
		messenger = &copy
	}
	delivery := editorcounts.Delivery{Enabled: cfg.ApprovalsNotify, Messenger: messenger, Contacts: router.Contacts, Template: cfg.EditorCycleTemplate, Language: cfg.EditorCycleLanguage, Link: web.Link}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if web.Handles(r) {
			web.ServeHTTP(w, r)
			return
		}
		if web.Asset(w, r) {
			return
		}
		portal.ServeHTTP(w, r)
	}), func(ctx context.Context) { service.Run(ctx, delivery, log) }
}

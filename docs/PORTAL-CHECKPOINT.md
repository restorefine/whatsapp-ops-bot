# Handoff checkpoint: RestoRefine client video portal

**Latest update, 3 November — deployment and editor counts:** the user has now
built/deployed the original portal on Hetzner, transferred credentials, published
OAuth to In production, passed the container Drive access check and rehearsed
Masala approval notifications and timestamped notes. The older local-only status
below is historical. Client notifications currently use open incoming WhatsApp
windows; templates are being prepared by the user.

A new editor-count upgrade has been implemented: `VIDEO_TEAM_WA_NUMBERS` roster,
admin-only `/editors [name]`, protected live report with Excel, fresh 0/20 cycles,
separate historical imports, immutable task/batch evidence, and automatic admin
payment-review alerts at each full cycle. Counts use completed ClickUp tasks,
not portal approvals, and never reset by month. This upgrade still needs the
server to pull/build/recreate the app and add its roster. See
[editor setup and admin usage](EDITOR-VIDEO-COUNTS.md). Local `.env` has the
user's three editor mappings; it is ignored and is not transferred by Git.
A fixture-only preview runs on localhost:8094; its counts are sample data.
Go race tests, vet, Next build, Docker build, desktop/mobile browser checks and
independent Excel parsing passed. No real WhatsApp alerts or production Drive
writes were made while building/testing this upgrade.


**Update, 3 November:** work resumed after this checkpoint. Bulk onboarding CLI,
Docker export/secret mounts, state rollback recovery, nonblocking client-window
lookups, invalid filename holds and network sends outside the state mutex have
now been implemented. New tests, Go vet, Linux build and a Docker image build
passed. The saved Google connection was checked successfully. A clearly named
Portal Test Client and a NEW generated test video have now been created only
inside the authorised root for end-to-end verification. Existing client media
remains untouched. Production is not deployed. The full real client roster,
approved Meta templates and Google Production publishing are still required.
See [current live setup](PORTAL-LIVE-SETUP.md); the historical notes below describe
the earlier handoff and are superseded where these completed items differ.

The real end-to-end browser check also passed: authenticated Drive Range streaming,
timestamp note saved and retained after reload, seek from a saved note, inline
approval, and the actual new test file moved/renamed into Approved. The local
Drive-backed verification server was stopped afterward to avoid a second writer.
The local Next.js demo can remain running. No real WhatsApp messages were sent.

Saved 2 November 2026 at the user's request to continue with another agent.
Read this document before changing anything. This is an unfinished local build,
not a deployed or production-ready portal. All work is uncommitted.

## Workspace and production constraints

Repo: `/Users/prabishdangi/Desktop/Personal/RestoRefine-UK/whatsapp-ops-bot`.

- Existing Go bot relays ClickUp/Metricool to WhatsApp Cloud API. Production is
  `https://bot.restorefine.co.uk` through the existing Cloudflare Tunnel on a
  shared Hetzner host, Linux user `opsbot`.
- Never touch the separate saucy-menu stack, its ports, Caddy or containers.
  Production uses this repo's `docker-compose.tunnel.yml`.
- No database. Review workflow state belongs in Drive; memory is transient.
  Local OAuth credentials/tokens/signing keys are secrets, not business storage.
- Existing `/remind <team member>` task reminders MUST remain free. Outside
  the incoming 24-hour window they return a tap-to-send wa.me link.
- Paid-template fallback IS authorised for portal notifications/reminders,
  including clients, Suranjana, Rohit and Harpreet, and `/remind videos <client>`.
  Do not ask again whether these portal costs are acceptable.
- Keep the existing founder morning task reminder paused by default.
- Commits must contain no AI/Claude attribution. No commit or deployment has
  been made during this task.
- `.vscode/` was already untracked before our work. Preserve it.

## Absolute Drive boundary

User explicitly said: do not delete, move, rename or otherwise affect existing
videos/folders. Work ONLY inside the new folder they supplied:

`https://drive.google.com/drive/u/1/folders/10s2NdCuhBiFBXsWa_BGgCxC9gxTElWV0`

Authorised root ID: `10s2NdCuhBiFBXsWa_BGgCxC9gxTElWV0`.
Drive name: ` RestoRefine Ops Bot` (leading space exists).
Enclosing Shared Drive ID: `0ABkqMHHcZc6DUk9PVA`; name shown in screenshot is
`3. Content & Video Drive`. That Shared Drive contains existing client folders
and `Calender/Sep Calendar/` PDFs. Those siblings are OUTSIDE authorised scope.

Only read-only verification has happened on real Drive. The authorised root
was empty and writable when checked. No Drive files/folders/permissions have
been created, changed or deleted during this session. Backend tests use fakes.

The bot may eventually organise NEW previews intentionally submitted inside
the authorised root. It must not discover/import/move existing sibling videos.
Reject shortcuts and arbitrary/outside-root file IDs. Never add a file-delete
method. The private streaming implementation does not require public sharing.

## Decisions already settled

- Google account OAuth, not an app password or service account. The user has
  Content manager access to the Workspace Shared Drive.
- Client portal frontend MUST use Next.js (user explicitly overrode an earlier
  Go-template proposal). Go remains responsible for Drive/API/WhatsApp.
- Match the homepage of the existing website project:
  `/Users/prabishdangi/Desktop/Personal/RestoRefine-UK/resto-website/restorefine-website`.
  That project is a reference only; we have not edited it.
- Brand: white/off-white surfaces, charcoal, red `#dc2626`, Inter / Inter Tight.
  The reference also uses Holiday script, but the portal's promotional script
  headline was removed after the user's feedback.
- Each client has a permanent private link, with rotation/revocation support.
  Clients can watch, approve, request changes or add notes without deciding.
- Calendar PDFs are manual reference. Suranjana transfers the posting date into
  filenames: `2026-11-05 - Venue tour.mp4`; revisions use
  `2026-11-05 - Venue tour - v2.mp4`. No AI/PDF extraction is needed for v1.
- Planned folders: `Clients/<Client>/<YYYY-MM>/01 - For review`,
  `02 - Approved`, `03 - Changes needed`, and private `_Portal/state.json`.
- New submission notifies the client's individual WhatsApp contact(s).
  Pending-review reminders also notify Suranjana, Rohit and Harpreet.
  Never auto-approve. Stop reminders on decisions/supersession.
- Automated messages go individually. Existing WhatsApp groups can have a
  pinned portal link. Current coexistence number cannot use Meta's Groups API.
- Proposed reminder defaults: first chase after 24 hours, then daily, urgent
  before posting. Exact times were not explicitly confirmed. Current code uses
  24-hour routine chases, urgent within 24 hours of posting with a 6-hour gap,
  UK time, existing `POSTING_DEADLINES` / 17:00 fallback. Confirm before live use.
- Client names/numbers, approved Meta template names, and final live deadlines
  are still missing. Do not send to guessed clients or contacts.

## User's latest UI instructions (preserve all)

1. Text was tiny. Use readable sizes: body/input ~16px, captions ~13px, clear
   hierarchy and usable buttons, including mobile. No microtype.
2. Remove AI slop. They explicitly requested taste-skill. Remove marketing
   slogans, decorative eyebrows/badges above titles, fake dropdowns, redundant
   pills/status decorations. Current H1 is simply the client name.
3. Videos are mostly REELS: the actual player CARD must be 9:16, compact and
   portrait, not a vertical video inside a giant landscape black container.
   The user saw the fix and said "nvm you fixed it"; keep it.
4. Feedback and change requests sit beside the video on desktop. No review
   decision dialogs. Changes/approval confirmation now happen INLINE in the
   feedback panel. Only the informational Help UI still uses Radix Dialog.
5. Scrub/pause the video, capture `currentTime` in notes, and click a saved
   timestamp to seek to that moment. Composer has an attach-time checkbox and
   a time button to update from the current playback position.
6. Use the actual white SVG mark for RestoRefine comment avatars, not a typed R.
   Current comments use `/portal/restorefine-logowhite.svg` inside a dark circle.
7. A red textarea focus outline was clipped by the containing panel. Fixed with
   an INNER focus border: `outline:none; border-color:var(--red);
   box-shadow:inset 0 0 0 1px var(--red)` on the inline composer.

Timestamp capture cannot inspect the cross-origin Drive iframe. We explained
the change to the user: use a native HTML5 player and stream Drive video bytes
through the Go bot, with Range support. This replaces the earlier embedded
Drive/public-link playback idea and uses server/tunnel bandwidth.

## Google connection: already done; do not repeat setup

Cloud project: `RestoRefine Ops Bot`, ID `global-impulse-416217` (reused an old
project because pending deletions continue counting toward the project quota).
Drive API enabled. OAuth audience External/Testing with the user's test account.
OAuth client is Web application; registered callback is exactly:

`http://localhost:8091/oauth/google/callback`

Downloaded client file:
`/Users/prabishdangi/Downloads/client_secret_88034049033-clpl1g3hus2pajj03oouiovfvkkapkvr.apps.googleusercontent.com.json`

Successful authorisation saved `.secrets/drive-token.json`, mode 0600, with a
refresh token. `.secrets/` and `client_secret*.json` are excluded from Git AND
Docker. Never print the credential/token contents. The temporary auth helper
has exited after success.

External Testing refresh tokens for Drive expire after seven days. Resolve
OAuth publishing/verification or an appropriate internal production setup
before unattended production. Do not promise the current testing grant lasts.

No portal signing-key file has been generated yet. No downloaded client secret
has been copied into `.secrets/drive-client.json` yet (that is merely the config
default). Use the real Downloads path locally or securely install a copy later.

## What exists in the working tree

### Frontend, running and interactive with DEMO data

`portal/`: Next.js 16.4.4, React 19, TypeScript, custom CSS, Radix Dialog, Lucide.
Uses App Router, `output:"export"`, `basePath:"/portal"`, `trailingSlash:true`.
The intention is to serve its export from Go in the SAME existing bot container,
without a separate production Node service.

- `portal/app/layout.tsx`, `app/page.tsx`, `app/globals.css`.
- `portal/components/studio.tsx`: tabs, Reel player, notes, timestamp capture /
  seek, inline approve/change modes, empty/loading/error states, Help dialog.
- `portal/lib/types.ts`: models and explicitly labelled demo workspace.
- `portal/public/`: copied official black/white SVG marks, locally downloaded
  Inter / Inter Tight WOFF2, Holiday font and three reference-brand photos.
  Source website assets were not modified.
- `portal/public/demo-preview.mp4`: generated 12-second portrait sample from a
  brand photo. It is a demo, not an actual client video. Current demo previews
  all point to it. Demo notes/decisions are browser memory only.
- In development `app/page.tsx` passes `localPreview=true`; it intentionally
  uses demo data and does NOT hit Go or real Drive. A production export passes
  false and fetches same-origin `/api/portal`.
- `portal/AGENTS.md` was generated by Next dev; read it. Relevant framework
  guides are in `portal/node_modules/next/dist/docs/`. `next-env.d.ts` is generated.

LOCAL PREVIEW IS CURRENTLY RUNNING, HTTP 200 verified:

`http://localhost:3001/portal/`

Start it if needed (avoid starting a duplicate if port 3001 is occupied):

```bash
cd /Users/prabishdangi/Desktop/Personal/RestoRefine-UK/whatsapp-ops-bot/portal
npm run dev
```

The current tool exec session for dev is 92164; session IDs may not survive
handoff, so check the URL/port rather than depending on that ID.

### Go authentication helper (complete and tested)

`cmd/drive-auth/main.go`, `main_test.go`: one-time local OAuth login, loopback
listener, PKCE + browser state validation, timeout, private exclusive token write.
Never logs secrets. `golang.org/x/oauth2 v0.30.0` was selected to retain Go 1.23
compatibility. `go.mod` / `go.sum` include it and Google compute metadata.

### Go portal backend (new WIP; tests pass, needs review / integration completion)

- `internal/drive/client.go`: pinned authorised-root client; fresh ancestry
  checks; folder creation; private JSON records; move/rename; native video
  streaming with forwarded Range and partial-content headers; token refresh
  persistence; rejects public root/record permissions; no file-delete API.
- `internal/approvals/service.go`: Drive state schema, clients/month folders,
  filename parser, new-video polling, revisions, timestamped notes, idempotent
  decisions, durable move intent/reconciliation, per-client access checks,
  contact tracking and link generation rotation counter.
- `internal/approvals/http.go`: signed permanent links at `/c/<token>`, HttpOnly
  30-day sessions, clean redirect to `/portal/`, per-client API DTOs, CSRF and
  Origin checks, private static export serving, authenticated video endpoint.
  It deliberately excludes numbers/internal state from client JSON.
- `internal/approvals/notifications.go`: initial/reminder/internal outbox,
  window-aware free messages / separate approved paid templates, held ambiguous
  sends, 30-second processing / two-minute scan, manual video reminder path.
- `cmd/server/approvals.go`: feature-gated setup, named Suranjana/Rohit/Harpreet
  recipient lookup, bot integration and one-attempt portal messenger copy.
- `cmd/server/main.go`: mounts `/c/*`, `/portal/*`, `/api/portal*`, adds client
  window tracking and starts the approval service only when enabled.
- `internal/config/config.go`, `.env.example`: new `APPROVALS_*` settings.
  BOTH `APPROVALS_ENABLED` and `APPROVALS_NOTIFY_ENABLED` default FALSE.
- `internal/commands/remind.go`, `router.go`: separate admin namespace
  `/remind videos <client>`; existing team task reminder path preserved.
- `internal/whatsapp/webhook.go`: `AdditionalNumberAllowed` tracks configured
  client windows; Router still assigns clients RoleNone so no staff command
  privileges or unwanted staff help replies.

## Verified checks and artifacts

Latest `go test -race ./...` PASSED for the whole repository, including new tests:

- `internal/drive/client_test.go`: outside-root writes/streaming blocked,
  shortcuts blocked, allowed video Range response forwarded.
- `internal/approvals/service_test.go`: filenames, notes/timestamps, cross-client
  isolation, durable-save rollback for an existing review, idempotency, racing
  decisions, moved-to-other-client stream refusal, CSRF, link/session rotation,
  free vs template sending and no repeat after uncertain delivery.
- `internal/commands/video_remind_test.go`: admin-only dispatch and continued
  free-only team reminder behaviour.

`npm run typecheck` passed. `npm run build` passed, producing `portal/out/`.
`git diff --check` passed. The local portal URL returned 200 at checkpoint time.

Browser verification script: `.data/portal/check.cjs` (ignored artifact, useful
to continue). It imports existing Playwright from:
`/Users/prabishdangi/.npm/_npx/e41f203b7505f1fb/node_modules/playwright`.
Run with `node .data/portal/check.cjs`.

Browser checks PASSED: 360x640 player (exact 9:16), real white SVG, scrub to 5s
then note captures 00:05, clicking timestamp seeks, changes/approval have no
dialog, mobile 390px has no horizontal overflow, mobile inputs compute to 16px,
and no JS/network errors. Screenshots `.data/portal/desktop.png` and `mobile.png`.
These screenshots precede the final tiny focus-border fix; retake when useful.

Go vet, Linux build, Docker build and a REAL Drive end-to-end review have NOT
been run. Production has NOT been touched. No WhatsApp portal messages sent.

## Important unfinished work / known review findings

Do not simply enable the flags or describe this as production ready.

1. **Fix transaction rollback for newly added map keys.**
   `Service.transaction` currently unmarshals the old JSON into the existing
   `s.state`. Go JSON unmarshalling into a non-nil map retains keys absent from
   the old JSON. A failed poll/onboarding save could leave newly added review
   keys in memory. Restore into a fresh `State` and assign it, then add a test
   for failed durable NEW review registration. Existing-note rollback test passes
   but does not catch this case.
2. **Review lock scope and webhook acknowledgement.**
   Service holds a global mutex during Drive scans and WhatsApp sending.
   `AdditionalNumberAllowed -> KnownNumber` also locks it. An incoming client
   webhook can therefore block behind long network work before acknowledgement.
   Use a separate immutable/atomic contact snapshot or split the locking so
   client window tracking remains fast. Keep a single durable state writer.
3. **Finish invalid rename behaviour.** Existing pending names can update dates
   within the same month/version, but invalid renamed previews can remain shown
   using the old date; make these held/unannounced consistently, clear the invalid
   marker when corrected, and ensure stale reminder jobs do not use old dates.
4. **Review recovery semantics and state validation.** Enforce a nonempty valid
   destination and client/month reference on persisted move intents; handle
   transient Drive failures safely; avoid a success state that hides unfinished
   moves. Persistent state is one JSON file, one app instance; concurrent setup
   CLI + bot writers must be prevented/documented. Bound state/outbox growth.
5. **Build an onboarding/setup CLI.** There is NO `cmd/approvals-setup` yet.
   It needs to create/read a private persistent signing key, initialise the
   service inside the authorised root, add clients/contacts/month folders,
   print their links, and optionally rotate a link. No actual clients or signing
   key have been created yet. Do not infer contacts from root sibling folders.
6. **Docker/serving integration not finished.** `Dockerfile` has not yet been
   updated with a Node build stage and a copy of `portal/out` into the Go runtime.
   Compose has no credential/key mounts yet. Configure `APPROVALS_STATIC_DIR`
   accordingly, preserve non-root permissions and safe token refresh writes.
   The optional bundled `Caddyfile` still only allows webhook/health; tunnel mode
   goes straight to Go and must not involve the separate saucy-menu Caddy.
7. **Frontend cleanup / final checks.** `globals.css` has initial styles plus
   iterative overrides and unused marketing selectors. Consolidate carefully
   without undoing the user's latest visual choices. Header logo dimensions
   were originally set like a horizontal wordmark although the SVG is the square
   R mark; consider using the mark at its correct ratio with normal brand text.
   Team comment avatar is already fixed. Poll client data gently when idle;
   don't discard a typed note or interrupt playback. Real live data has no
   poster/duration metadata yet. Test loading/error/expired link states and live
   timestamp persistence through the Go API, not only demo memory.
8. **Finish notification operations.** Add tests for reminder timing/DST,
   staff-recipient mapping, superseded/decided jobs, explicit closed-window
   fallback, and client webhook window tracking without command access.
   Missing templates currently hold outbox work; real recipients/templates need
   onboarding. Expose a safe way for staff to inspect/retry uncertain sends;
   exact-once Meta delivery is not possible. Avoid blind paid retries.
9. **Update documentation to current architecture.**
   `docs/video-approval-portal-plan.md` is an EARLIER plan and still says Go HTML
   templates / embedded Drive / temporary public sharing in places. This
   checkpoint overrides those stale design details: Next.js export, native
   streaming, timestamp notes and inline Reel review are current.
   README only documents OAuth setup so far; add actual portal setup/testing/
   deployment commands and update the guide/help where appropriate.
10. Resolve OAuth External Testing expiration and obtain client contacts/template
    approvals before production rollout. The user performs server SSH commands
    because their SSH key needs a passphrase. Prepare concrete changes and
    validation locally before giving deployment instructions.

## Skills / environment notes

- Used `design-taste-frontend` at
  `/Users/prabishdangi/.agents/skills/design-taste-frontend/SKILL.md` and
  `better-typography` at
  `/Users/prabishdangi/.agents/skills/better-typography/SKILL.md`.
  The taste skill calls itself `tasteskill`; user explicitly asked for it.
  Its own scope excludes dense app UI; use relevant brand/type/form principles,
  not landing-page slogans or oversized hero structures.
- Node v24.13.1, Go 1.23.5. Next version checked with npm: 16.4.4.
- Session instructions prohibit spawning subagents unless explicitly authorised
  by the user or applicable skill/AGENTS.
  No subagents have been used.
- Go/npm caches and browser processes required sandbox escalation. Approved
  prefixes now include `npm install`, `npm view`, `npm run dev`, `go test`,
  `go get`, `go mod tidy`, `go run ./cmd/drive-auth`, and the Playwright script.
- Python's default HTTPS CA setup failed verification; read-only Drive checks
  succeeded with `ssl.create_default_context(cafile='/etc/ssl/cert.pem')`.
  Do not disable certificate verification or print bearer credentials.

## Suggested next action

Read the current files, fix the rollback and client-window lock issues, then
finish onboarding and Docker integration. Keep the local preview available while
iterating. Verify the production export against Go with a clearly labelled test
client/new test video ONLY inside the authorised root. Leave real WhatsApp sends
disabled until contacts/templates are configured. Preserve all user UI fixes.

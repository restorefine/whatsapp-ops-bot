# Client video approval portal: build plan

This is the original design plan. The current implementation uses a Next.js
export, private native-video streaming, timestamped notes and inline decisions.
Use [PORTAL-LIVE-SETUP.md](PORTAL-LIVE-SETUP.md) for current deployment instructions;
its architecture supersedes the earlier template/public-preview proposals here.

## Status and agreed scope

Design decisions recorded on 2 November 2026. The local Google OAuth connection
works and the authentication helper's tests pass. The portal, Drive poller and
video reminders have not been implemented. No Drive content has been changed.

Build the portal into the existing Go bot at `bot.restorefine.co.uk`, using the
existing Cloudflare Tunnel. Drive holds workflow state; there is no database and
no ClickUp integration for video approvals. Existing ClickUp commands continue.

The client watches previews, approves, requests changes or adds notes. Suranjana
submits previews by placing them in a review folder. Founders do not have to
forward each video, and clients do not need to contact staff directly.

## Mandatory Drive boundary

The only authorised root is:

- Name: `RestoRefine Ops Bot` (the current Drive name has a leading space).
- Folder ID: `10s2NdCuhBiFBXsWa_BGgCxC9gxTElWV0`.
- Link: <https://drive.google.com/drive/folders/10s2NdCuhBiFBXsWa_BGgCxC9gxTElWV0>.
- Verified: empty; the connected account can add children, edit and share.

User instruction: work only inside this folder. Existing client video folders,
calendar PDFs and other Drive content are outside scope. Do not scan, import,
delete, move, rename or alter their permissions. Do not follow shortcuts to them.

Implement this restriction in the Drive adapter before enabling polling:

- Pin the root by ID, not by a name search or the enclosing Shared Drive ID.
- Discover children only from the root and verified descendants.
- Before reading video content or changing a file, verify both its ancestry and
  that it is registered to this workflow/client. Recheck before mutations.
- Verify source and destination for moves. Reject outside IDs, shortcuts,
  trashed items, unexpected parents and attempts to move the root.
- No Drive file-deletion operation is needed or exposed by the portal.
- OAuth credentials allow broader access than this folder: application checks
  enforce the boundary. The OAuth consent screen is not a folder restriction.

New previews intentionally submitted inside the workflow can be moved and
renamed after decisions, as agreed. Their original media bytes are preserved.

## Folders and filenames

```text
RestoRefine Ops Bot/
  Clients/
    <Client>/
      2026-11/
        01 - For review/
          2026-11-05 - Diwali special.mp4
        02 - Approved/
        03 - Changes needed/
  _Portal/
    clients.json
    reviews/
      <review-id>.json
```

`_Portal` holds private configuration and review records, never public link
permissions. Create real client folders only from an explicit client list;
first implementation tests use a clearly labelled test client in this root.

The chosen submission format is `YYYY-MM-DD - Video title.mp4`. The date is the
planned posting date, interpreted in `Europe/London`. The date picker and PDF
parsing are unnecessary for v1. PDFs remain the team's reference calendar.

- Validate actual calendar dates, media type and the client's month folder.
- Invalid filenames stay private and unannounced; tell Suranjana what to fix.
- A valid date in the past is visibly overdue, rather than silently changed.
- Persist posting date and original filename before adding decision prefixes.
- First upload is v1. Revisions use the same date/title and a suffix, such as
  `2026-11-05 - Diwali special - v2.mp4`, with a new Drive file ID.
- Match revisions by client/date/title and explicit version. Ambiguous duplicates
  are held for correction, rather than guessed or allowed to replace approvals.
- Each version keeps its own feedback and decision history.

## Review workflow

1. Poll the configured review folders every two minutes. Use bounded pagination,
   backoff and a short-lived cache. Do not scan the whole Shared Drive.
2. Register a completed, valid video using its Drive file ID, stable review ID,
   original name, posting date, revision and client. Wait for Drive preview
   readiness; do not send a client to a known unplayable preview.
3. Add a file-specific `anyone` reader permission while review is pending, if
   the Shared Drive's policy permits it. Keep client folders and records private.
   If access is inherited or sharing cannot be safely reversed, hold the file
   and report the setup issue. Do not widen parent or Shared Drive permissions.
4. Notify the client's configured individual WhatsApp contact(s), including the
   permanent portal link. Record the notification so polling does not repeat it.
5. The portal lists pending videos across months, with title, posting date,
   version, Drive preview and existing notes. It also shows decision history;
   completed versions have their preview disabled after sharing is revoked.
6. Approve accepts an optional note. Request changes requires a note. Add a note
   leaves the status pending. Notes have timestamps, belong to one version, and
   notify Suranjana. Plain-text timestamps such as `00:12` are supported; frame
   drawing and timed annotation playback are out of scope for v1.
7. For a decision, durably record it, remove only the temporary permission this
   workflow created, move the preview into Approved or Changes needed, and add
   a readable decision/date/time prefix. Preserve the original filename in the
   record; put the latest requested changes in the file description without
   losing the complete notes history.
8. Notify Suranjana of the result. A revised video starts another pending round.
   Stop reminders for completed or superseded versions. Never auto-approve.

Drive's [sharing rules](https://developers.google.com/workspace/drive/api/guides/manage-sharing)
mean inherited access cannot simply be revoked on a child. Track the exact
permission created by the bot; never remove unrelated permissions.

## Portal access and durable state

- One permanent, unguessable link per client, suitable for pinning in their
  existing WhatsApp group. Possession grants access; no Google login is required
  for the client. Support individual link rotation/revocation.
- Use opaque client IDs and a dedicated persistent signing secret, with a
  per-client generation for revocation. A link cannot select arbitrary Drive IDs.
- Exchange the link for an HttpOnly session and redirect to a clean page URL.
  Do not log bearer links, cookies, notes or credentials. Disable caching and
  referrer leakage, including for the embedded Drive player.
- Every video action verifies the client, current version, current pending
  status and folder boundary. Use POST, CSRF protection, request size limits,
  escaped notes, idempotency IDs and per-review locking.
- Drive JSON records hold decisions, note history, revision links, notification
  progress and any incomplete moves or permission changes. Small file
  [app properties](https://developers.google.com/workspace/drive/api/guides/properties)
  can point to these records; do not store whole conversations in properties.
- Run one bot instance. Persist intent before a multi-step transition and
  reconcile incomplete work after restart; Drive is not a transaction database.
- Treat sharing revocation failure as incomplete work, surface it to staff and
  retry safely. Do not silently report a private/completed state prematurely.

## WhatsApp and reminders

Automated messages go to individual numbers. Existing WhatsApp groups can hold
the pinned portal link, but are not part of this bot's automated sending path.

- New-review notifications go to the client's configured approval contacts.
- Pending-review reminders go to those contacts plus Suranjana, Rohit and
  Harpreet. Group multiple pending videos into one message per recipient where
  practical.
- Use tracked individual incoming-message windows first. Use approved paid
  templates outside the window for portal alerts, including staff alerts and
  manually triggered video reminders. This fallback is explicitly authorised.
- Add client numbers to window tracking, with no admin/team command privileges.
  The current webhook only accepts admin/team numbers, so this needs an explicit
  change. Ordinary client conversations should not receive staff help text.
- Persist notification progress in Drive. After restart, conservative unknown
  windows use the approved template path. Avoid automatic repeated sends when
  the result is ambiguous; exact-once delivery across Drive and Meta is not
  guaranteed. Only explicit closed-window failures trigger a template fallback.
- Add `/remind videos <client>` for admins. It sends one grouped reminder for
  that client's currently pending versions and includes the portal link. Return
  a useful message without sending if nothing is pending.
- Existing `/remind <team member>` task reminders keep their free-only policy,
  including the tap-to-send fallback, and must never call paid templates.
- Keep the paused founder daily task reminder unchanged.

Proposed configurable defaults for initial implementation (timing has not yet
been explicitly confirmed): first chase 24 hours after notification, then daily;
an urgent chase 24 hours before the planned posting deadline. Use UK time and
combine overlapping scheduled reminders. For date-only filenames, propose a
17:00 posting deadline with per-client overrides. Confirm client-specific
posting times before live reminders; the existing posting checks already have
some different deadlines. Pending reviews remain pending after the deadline.

Keep portal template names separate from the existing founder reminder template.
New-review, pending-review and internal-alert messages need suitable approved
templates. The current messenger supports body variables; add URL-button support
only if needed by the approved template format.

## Implementation sequence

1. **Drive adapter and onboarding.** OAuth refresh support, private mounted
   credentials, root guards, explicit folder provisioning and client config.
   Inspect inheritance/sharing and prove the boundary using only a new test
   file within the authorised root. Implement no file-deletion API.
2. **Workflow and recovery.** Filename parser, registration, version matching,
   Drive records, notes, transitions, permission cleanup and restart recovery.
3. **Portal.** Responsive Go templates and static assets, client sessions,
   pending/history views, preview and protected action routes. Add routes under
   `/c/` and `/portal/` to the existing chi router. No separate frontend service.
4. **Notifications.** Client window tracking, separate portal sender, approved
   template fallback, durable reminder progress and `/remind videos <client>`.
5. **Pilot and deployment.** Exercise a test client in dry-run mode, then one
   real client with configured contacts, then onboard the remaining clients.
   Prepare user-run deployment commands using the existing tunnel compose setup.

Likely code ownership: new `internal/drive` and `internal/approvals` packages;
changes to `internal/config`, `internal/commands`, `internal/whatsapp`, and
`cmd/server/main.go`; embedded portal templates/assets; setup documentation.

## Verification and production setup

Required checks cover real failure modes: outside-root and cross-client IDs;
shortcuts and files moved out during review; invalid/ambiguous filenames; replayed
or concurrent decisions; superseded versions; escaped notes and CSRF; permission
cleanup failures; restart during transitions; notification replay/cooldowns; UK
date/DST handling; and proof that team task reminders never invoke templates.
Run the existing suite with `go test -race ./...`, Go vet and a Linux build.
Check the portal on a phone and laptop with a test video.

Before live rollout:

- Obtain the active client names and their individual approval contacts.
- Set Suranjana/Rohit/Harpreet recipients from explicit named admin mappings.
- Confirm reminder times and per-client posting deadlines.
- Resolve External OAuth Testing's seven-day refresh-token lifetime before
  relying on unattended production access; do not repeatedly reconnect weekly.
  See [Google's token rules](https://developers.google.com/identity/protocols/oauth2#expiration).
- Get the required Meta templates approved and configure their exact language
  and parameters. Keep real sending off during tests.
- Mount only credentials/token files as secrets, readable by the existing
  distroless non-root process. Do not bake them into the image or commit them.
  Support safe token refresh and replacement where required.
- Keep persistent portal signing secrets across restarts. Document reconnecting
  Google access and rotating one client's portal link.
- Deploy through `docker-compose.tunnel.yml` and the existing hostname. No new
  host listeners or changes to the separate saucy-menu stack. The optional
  bundled Caddy currently permits only webhook/health routes; adjust its route
  allowlist only if that deployment mode will also be supported.
- Initially deploy with the portal feature and real notifications disabled,
  enable the test client, verify end-to-end, then enable intended real clients.

The completed setup work is `cmd/drive-auth`, private local token storage and
credential exclusions in Git/Docker. This document plans the remaining portal
build; it does not claim those parts are already running.

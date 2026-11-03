# Editor video counts

Admins send `/editors` to the bot for the whole video team, or `/editors arpan`,
`/editors sunil` or `/editors himal` for one editor's summary. The reply includes
a private live report link. Excel download is available on that page; there is
no download confirmation conversation on WhatsApp.

## Initial setup

In the server `.env`, add a roster using the same name=number format as team
contacts. Use the editor's ClickUp first name and country-code number:

```dotenv
VIDEO_TEAM_WA_NUMBERS=arpan=EDITOR_NUMBER,sunil=EDITOR_NUMBER,himal=EDITOR_NUMBER
EDITOR_CYCLE_TEMPLATE=
EDITOR_CYCLE_TEMPLATE_LANG=en_GB
```

Replace `EDITOR_NUMBER` with real digits-only numbers. The roster is separate
from roles: it grants no command access. An editor may appear in both this list
and `TEAM_WA_NUMBERS`. Only numbers already in `ADMIN_WA_NUMBERS` / the legacy
owner list can request or open the admin report. Never add editors as admins
just to count their work.

The existing portal must be enabled, with its Drive credentials, persistent
signing key and secrets mount. Keep `APPROVALS_ENABLED=true` and
`APPROVALS_NOTIFY_ENABLED=true`. Existing named mappings for Suranjana, Harpreet
and Rohit supply the alert recipients. No new database or port is needed.

For the first installation, pull the updated code and build only the bot app:

```bash
cd ~/whatsapp-ops-bot
git pull --ff-only
export OPSBOT_UID="$(id -u)"
export OPSBOT_GID="$(id -g)"
export COMPOSE_FILE=docker-compose.yml:docker-compose.tunnel.yml:docker-compose.portal.yml
docker compose config --quiet
docker compose build app
docker compose up -d --no-deps --force-recreate app
docker compose logs --tail=60 app
```

Do not run these commands from, or modify, the separate saucy-menu stack.
Later roster changes need only an `.env` edit and app recreation, without a
rebuild. A newly added editor gets their own fresh baseline on the next scan.
Removing an editor retains their old records under an inactive entry.

## Counting and audit rules

- On the first successful full scan, each configured editor starts at **0/20**.
  Existing completed tasks are stored as historical evidence, outside the new
  cycles. They generate no automatic payment alerts.
- One completed top-level task in the editor's matching personal ClickUp list
  counts as one video. Current lists were verified as Arpan, Sunil and Himal.
  The task must also be assigned to the matching ClickUp member.
- Status must be Complete, Completed or Done, with a done/closed status type
  and an actual `date_closed` or `date_done` timestamp. “Hold”, cancelled,
  review, subtasks, multiple video-editor assignees and missing timestamps do
  not count. `date_updated` is not treated as completion evidence.
- Tasks are fetched across all accessible history with no month filter.
  Pagination errors and page-limit truncation fail the refresh instead of
  presenting a partial total as complete. Archived/deleted/inaccessible work
  that the API does not return cannot be reconstructed from scratch.
- New tasks are ordered by completion time, then task ID, and appended once.
  Batch 1 contains its first 20 IDs; the 21st is batch 2, position 1. Later
  discoveries are appended without changing earlier batch membership.
- Each audit record includes task ID, original title, list, original status,
  completion timestamp, time recorded, batch/position and a ClickUp proof link.
  Proof links require the admin's existing ClickUp access. The report verifies
  task completion, not the contents of the exported video.
- A reopened, reassigned, missing or timestamp-changed task stays in its
  original audit position and gets a visible issue. It never counts twice.
  An unsent payment-ready alert is held while its batch has an evidence issue.
  Already delivered alerts cannot be recalled; review the current evidence.
- Full batches mean **ready for payment review**, not paid. The bot makes no
  payment and has no mark-paid action. All earlier cycles stay visible.

The audit ledger and notification receipts are stored in the existing private
Drive `_Portal/state.json`, through the same transaction and process lock as
video approvals. Nothing outside the authorised portal root is written. Run
only one Drive-backed bot instance. Preserve that state and the signing key.
Do not roll back to older binaries that do not preserve the editor ledger.

## Automatic alerts

The bot checks ClickUp about once a minute. When a new cycle reaches 20 verified
records, it queues one individual message per configured admin, including a
private evidence-report link. Editors are never sent these notifications.

Example:

> Arpan has completed batch 1: 20 videos. Ready for payment review.
> The next cycle has started; previous batches are kept for audit.
> Evidence report: [private admin link]

With `EDITOR_CYCLE_TEMPLATE` empty, each alert waits until that admin has an open
incoming-message window. Admins can message the bot directly to open it. Group
messages do not provide these individual windows, and windows are in memory,
so admins need to message again after a restart.

For automatic delivery outside that window, submit this text-only template in
WhatsApp Manager, Utility / English (UK), without header, footer or buttons:

Name: `rr_editor_cycle_ready`

```text
RestoRefine editor cycle update for {{1}}: {{2}}

Please review the completed video evidence before arranging payment.
```

Variable 1 example: `Arpan`. Variable 2 example:
`Batch 1: 20 videos completed; ready for payment review. Evidence: YOUR_ADMIN_REPORT_LINK`.
Meta determines approval and category. After approval, set
`EDITOR_CYCLE_TEMPLATE=rr_editor_cycle_ready` and recreate only the bot app.
Existing free-only `/remind <team member>` task reminders are unaffected.

Sending intent is saved before contacting Meta. Confirmed sends are not sent
again after a restart. A crash during sending or an ambiguous network error is
held as uncertain and shown on the report, rather than blindly retried. Check
those receipts manually. “Sent” means Meta accepted the API request; it is not
a delivery/read receipt. A full cycle triggers one notice, not a daily payment
chase.

## Admin report access

Report links are bearer credentials delivered only in replies to allowlisted
admins or their automatic cycle alerts. Keep them private. They expire after
15 minutes and can be redeemed once using **Open report**. WhatsApp previews
do not consume them. The resulting browser session lasts eight hours and is
separate from client review sessions. An admin removed from configuration loses
access after the app restarts. A restart expires unredeemed links; `/editors`
issues a new one. Reissuing a link replaces that admin's earlier unused links.

The live report and `.xlsx` endpoint both require that admin session. They are
not indexed or cached publicly. The page shows current progress, previous
cycles, historical evidence, flagged tasks and alert receipts. Excel includes
Summary, Video evidence and Counting notes sheets. Task names are written as
text, never executable spreadsheet formulas.

## Message to share with Suranjana, Harpreet and Rohit

> Send `/editors` to the bot for all video editor counts, or `/editors sunil`
> for one editor. Open the private report link to see the tasks behind every
> count, switch between cycles, or download Excel. Each new cycle has 20 videos
> and carries across months. We all get an alert when a cycle is complete and
> ready for payment review. Check the task evidence before arranging payment.
> These links are for admins only; don't pin them in client or editor groups.

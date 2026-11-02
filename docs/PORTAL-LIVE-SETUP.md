# Put the video portal live

The portal is a Next.js export served by the existing Go bot. It uses private
Drive streaming, filename dates, timestamped feedback and inline decisions.
This deployment uses the existing bot Cloudflare Tunnel and no new public ports.
Never run these steps in the separate saucy-menu directory.

## Prepare the client roster

Save a CSV as `.secrets/clients.csv`, with this header:

```csv
client,whatsapp
Actual client name,447XXXXXXXXX;977XXXXXXXXXX
```

Use the real active client names and approval contact numbers. Each number must
include its country code; separate multiple contacts with semicolons. A blank
contact field creates a usable private portal but cannot receive notifications.
The example in `portal-clients.example.csv` is a format example, not a client
list to onboard unchanged. The roster and generated links are private.

Onboarding creates only `Clients/<Client>/<Month>/01 - For review`,
`02 - Approved` and `03 - Changes needed` below the authorised folder
`10s2NdCuhBiFBXsWa_BGgCxC9gxTElWV0`. It never imports old client folders.
The bot provisions the current and next UK month and retains past review history.

## Create the three WhatsApp templates

In Meta Business Suite, open **WhatsApp Manager → Message templates → Create
template**, select the RestoRefine WhatsApp Business account, and submit these
three text-only templates as **Utility**, language **English (UK)** (`en_GB`).
Meta controls approval and final category. Do not enable paid sending until the
templates show Approved. No buttons, header or footer are required by this code.

Name: `rr_video_ready`

```text
Your RestoRefine video for {{1}} is ready for approval. Please watch it and approve it or request changes here: {{2}}
```

Name: `rr_video_reminder`

```text
Your RestoRefine video approval for {{1}} is still pending. Please review the videos here: {{2}}
```

For those two templates, example variable 1 is a client name and example variable
2 is their private portal URL, in the form `https://bot.restorefine.co.uk/c/...`.
Use the exact approved names and language in `.env` if Meta changes your choices.

Name: `rr_video_internal_update`

```text
RestoRefine video review update for {{1}}: {{2}}
```

Example variable 1: `Example client`. Example variable 2:
`Venue tour: changes requested at 00:12. Replace the opening shot.`

The bot sends normal text when an individual recipient's incoming-message
window is open. Otherwise it sends the relevant approved template. Internal
template updates are normalised to a short single line. Existing team TASK
reminders stay on their separate, free-only path.

## Make Google access suitable for unattended use

The current OAuth app is External / Testing. In Google Cloud, select project
`global-impulse-416217`, open **Google Auth Platform → Audience**, and change
Publishing status to **In production** using **Publish app**. This does not make
the Drive folder public. Follow any applicable verification requirements shown
by Google; publishing does not waive them.

Reconnect once AFTER publishing, since the current testing grant expires after
seven days. On the Mac, from this repository:

```bash
go run ./cmd/drive-auth \
  -credentials '/Users/prabishdangi/Downloads/client_secret_88034049033-clpl1g3hus2pajj03oouiovfvkkapkvr.apps.googleusercontent.com.json' \
  -output .secrets/drive-token-production.json
```

Complete Google's consent in the local link printed by the command. Keep the
existing redirect URI `http://localhost:8091/oauth/google/callback` for this
connection helper. Install the NEW token on the server as `drive-token.json`.
The Google account still needs its Content manager access. Clients do not sign
into Google. See [Google's token lifetime rules](https://developers.google.com/identity/protocols/oauth2#expiration)
and [OAuth publishing status](https://support.google.com/cloud/answer/15549945).

## Install on the existing bot server

These commands assume the code has been committed/pushed and you are logged in
as the existing `opsbot` Linux user. The agent cannot complete your passphrase-
protected SSH login on your behalf. Run commands inside `~/whatsapp-ops-bot`.

1. Update this repository and create the private directory:

```bash
cd ~/whatsapp-ops-bot
git pull --ff-only
install -d -m 700 .secrets
```

2. Transfer the following from the Mac using your existing SSH/SCP connection:

   - Downloaded OAuth client JSON → `.secrets/drive-client.json`.
   - New production token → `.secrets/drive-token.json`.
   - Existing `.secrets/portal-signing-key` → same server path, if local test
     links have already been created. Preserve this key; replacing it invalidates
     all existing private links. If missing, onboarding creates it once.
   - Completed client roster → `.secrets/clients.csv`.

All files must be owned by `opsbot`, with mode 600. The directory is mode 700.
Do not put secrets in the image or Git. The portal compose override runs the
container with the same UID/GID as this host user, so OAuth refresh can replace
the token file safely inside the mounted directory.

```bash
chmod 600 .secrets/drive-client.json .secrets/drive-token.json .secrets/clients.csv
```

3. Back up `.env` privately, then edit it. Preserve the existing WhatsApp,
ClickUp, Metricool and tunnel settings:

```bash
cp -p .env .secrets/env-before-portal
id -u
id -g
nano .env
```

Set the following, using the UID/GID printed above:

```dotenv
COMPOSE_FILE=docker-compose.yml:docker-compose.tunnel.yml:docker-compose.portal.yml
OPSBOT_UID=YOUR_NUMERIC_USER_ID
OPSBOT_GID=YOUR_NUMERIC_GROUP_ID
APPROVALS_ENABLED=true
APPROVALS_NOTIFY_ENABLED=true
APPROVALS_ORIGIN=https://bot.restorefine.co.uk
APPROVALS_NEW_TEMPLATE=rr_video_ready
APPROVALS_REMINDER_TEMPLATE=rr_video_reminder
APPROVALS_INTERNAL_TEMPLATE=rr_video_internal_update
APPROVALS_TEMPLATE_LANG=en_GB
```

The `YOUR_NUMERIC_...` values above are placeholders: replace them before running
Compose. `ADMIN_WA_NUMBERS` needs named entries for Suranjana, Rohit and Harpreet
to route portal alerts correctly. Preserve the existing admin/team role rules;
do not add clients to those lists. Client contacts come from the Drive registry.
`POSTING_DEADLINES` supplies UK posting times, with 17:00 as the default.
`REMINDER_TIME=off` stays unchanged. Confirm the intended posting times first.

If templates are not approved, use `APPROVALS_NOTIFY_ENABLED=false` only for
portal testing. That is not the requested final setup: live notifications should
be true once approved templates and real contacts are configured.

4. Build the updated image without stopping the existing bot:

```bash
docker compose config --quiet
docker compose build app
```

5. Stop only this bot's app for bulk onboarding, then restart it:

```bash
docker compose stop app
docker compose run --rm --no-deps --entrypoint /app/approvals-setup app \
  -clients-file /app/.secrets/clients.csv \
  -origin https://bot.restorefine.co.uk
docker compose up -d --no-deps app
```

The setup command prints each private client portal link and review-folder link.
It does not send WhatsApp notifications. The running bot owns delivery after
restart. The lock prevents setup and the bot from concurrently writing one Drive
state file. Avoid running a separate local Drive-backed portal writer once the
server is running; the local Next.js demo is fine because it never writes Drive.

If onboarding fails, restart `app` and inspect the error before retrying. Rerunning
the same roster updates existing named clients instead of creating duplicate
portals. An explicitly blank contact field clears that client's stored contacts.
Never remove the persisted signing key as part of a restart.

6. Verify:

```bash
curl -fsS https://bot.restorefine.co.uk/healthz
curl -sS -o /dev/null -w '%{http_code}\n' https://bot.restorefine.co.uk/portal/
docker compose logs --tail=80 app
```

`healthz` should return `ok`. `/portal/` without a private session should return
401; 503 means portal setup failed (the existing bot commands remain available).
Open a generated client link on a phone and verify its client name, upload a NEW
H.264/AAC MP4 named `YYYY-MM-DD - Video title.mp4` into that client's For review
folder, and check playback, timestamped notes, decision, Drive move and WhatsApp
delivery. Private link paths and tokens must not be shared in public logs.

## Operations

Suranjana submits NEW previews only inside the new review folders. Existing
videos in sibling client folders remain untouched. A revision must be a new file
with a suffix such as ` - v2.mp4`. The calendar PDF is not parsed.

The first routine chase is after 24 hours, then daily while pending. Urgent
reminders apply within 24 hours of the configured posting time, with a six-hour
gap from the previous message. Decisions and superseded versions stop reminders.
`/remind videos <client>` is admin-only; `/remind <team member>` retains its
free-only behaviour.

For additional onboarding, link rotation or uncertain delivery inspection, stop
only `app`, run the setup container, then restart `app`:

```bash
docker compose stop app
docker compose run --rm --no-deps --entrypoint /app/approvals-setup app -jobs
docker compose up -d --no-deps app
```

Use `-client 'Exact client name' -rotate` to replace that client's link, or
`-retry-job JOB_ID` only after checking the original uncertain delivery. The CLI
merely requeues it; the running app sends it. Preserve both the secrets directory
and Drive `_Portal/state.json` in backups.

To roll back portal enablement, set `APPROVALS_ENABLED=false` and
`APPROVALS_NOTIFY_ENABLED=false`, then recreate only `app`. Do not delete the
new Drive content or change anything in the separate saucy-menu stack.

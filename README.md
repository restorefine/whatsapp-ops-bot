# WhatsApp ops bot

A private, self-hosted WhatsApp bot that answers your commands with live data from ClickUp. Only the owner numbers you list get a reply. It stores nothing: each command fetches from ClickUp and sends the answer back.

## Commands

| Command | Reply |
|---|---|
| `/update` | Monthly overview: headline numbers, a team table (open, late and completed per person), work still overdue from earlier months, a day-by-day calendar of this month (✅ done, ⏳ open, ⚠️ overdue), a summary of social uploads, and next month's preview. |
| `/due` | Everything open that's due today and tomorrow: team tasks and social posts in separate sections, plus a count of older overdue work. `/today` works too. This is also the daily reminder. |
| `/uploads` | The social media posting calendar for this month, one post per line, with posted, upcoming and missed counts. It also lists earlier posts never marked as posted. `/uploads next` shows next month. |
| `/<name>` | One person's numbers, then overdue tasks, open tasks by status and what they completed this month. `/sunil`, `/sunil paudel`, or a prefix like `/su` all work. If a name is ambiguous, the bot lists the matches. |
| `/team` | Everyone in the workspace, and the exact command for each person. |
| `/help` | The command list. |

Commands are case-insensitive, the slash is optional, and trailing punctuation is ignored. Anything unrecognised gets the help text.

**Layout:** WhatsApp can't render real tables, so numbers and the team table go in monospace blocks (```), where columns line up. They're kept under 30 characters so they don't wrap on a phone. Task lists put the name on one line and the details below in italics. Long sections are capped with "+N more". Long replies are split into several messages, never inside a monospace block.

Example `/update` (shortened):

````
*📊 Monthly Update · October 2026*
_Sat 3 Oct 2026 · 21:56_

*Overview*
```
Due this month   21
Completed         3
Still open       14
Overdue           4
Overdue, older   27
```

*Team*
```
Name        Open  Late  Done
Sunil         35    18     0
Himal         18     2     2
Unassigned    12     7     0
```

*📅 October calendar*
*Sat 3 Oct* · _today_
⏳ Failte Graphic · Sagun · _To Do_

*📤 Social uploads*
```
Scheduled   118
Posted        8
Missed        4
Upcoming    106
```
````

**Uploads:** any task inside a ClickUp folder named `Uploads` (set by `UPLOADS_FOLDER`, or `none` to turn it off) is part of the posting calendar. Those tasks are kept out of the `/update` calendar and team table and summarised separately. "Posted" means the task is complete, and "missed" means its date has passed but it isn't complete.

## Daily reminder

Every day at `REMINDER_TIME` (default `08:00`, in `TZ`) each owner gets the `/due` report. Set `REMINDER_TIME=off` to turn it off. It runs inside the app, so nothing else needs scheduling. If the app is down at that minute, that day's reminder is skipped.

WhatsApp only allows free-form messages within 24 hours of the owner's last message to the bot. If an owner has messaged the bot in that time, they get the full list for free. If not, the bot sends the template named in `REMINDER_TEMPLATE` instead. It carries the counts and asks them to reply `/due`, and that reply reopens the window. With no template set, that owner is skipped for the day.

Create the template in **WhatsApp Manager → Message templates → Create template**. Use category **Utility**, name `due_reminder`, and language English (`en`). For example:

```
Good morning. {{1}} item(s) are due today and {{2}} due tomorrow. Reply /due to see the full list.
```

Once Meta approves it, set `REMINDER_TEMPLATE=due_reminder`. Template messages are charged per message (see Cost).

## How it works

```
WhatsApp ──► Meta Cloud API ──► Caddy (HTTPS) ──► app:8080 /webhook ──► ClickUp API
                  ▲                                     │
                  └──────── reply (Graph API) ◄─────────┘
```

- `POST /webhook` checks the `X-Hub-Signature-256` HMAC of the raw body (401 if it's wrong), drops anyone not listed in `OWNER_WA_NUMBER`, ignores repeat deliveries of the same message ID, returns 200 at once, and handles the message on a background worker.
- ClickUp calls use `GET /team/{id}/task` with pagination, a 60 second cache, and back-off on HTTP 429.
- Code layout: `internal/config`, `internal/whatsapp` (Messenger interface, Cloud API client, webhook), `internal/clickup` (client and `Client` interface), `internal/commands` (parser and reports, depending only on interfaces).

## Local development

Requirements: Go 1.23+, `openssl` and `curl`.

```bash
cp .env.example .env
# Fill in CLICKUP_TOKEN and CLICKUP_TEAM_ID. For WA_APP_SECRET, WA_VERIFY_TOKEN
# and OWNER_WA_NUMBER any values work locally. Set WA_DRY_RUN=true and
# LOG_LEVEL=debug so replies are printed instead of sent.
make run
```

In another terminal:

```bash
./scripts/simulate-webhook.sh "/update"        # signed like Meta signs it
./scripts/simulate-webhook.sh "/team"
FROM=15550001111 ./scripts/simulate-webhook.sh "/help"   # stranger: ignored
```

The reply shows up in the server log as `dry-run: message body`. To get real WhatsApp replies locally, set `WA_DRY_RUN=false` with real WhatsApp credentials. Meta can only reach your laptop through a tunnel such as `cloudflared tunnel --url http://localhost:8080`.

Make targets: `make build`, `make test`, `make lint` (uses Docker if golangci-lint isn't installed), `make run`, `make up`, `make down`, `make logs`, `make simulate TEXT="/update"`.

## Meta (WhatsApp) setup

1. Go to <https://developers.facebook.com/apps>, click **Create app**, choose the **Business** type, and add the **WhatsApp** product.
2. Open **WhatsApp → API Setup**. Meta gives you a free **test number**.
   - Copy the **Phone number ID** (not the phone number) into `WA_PHONE_NUMBER_ID`.
   - Under **To**, add your own WhatsApp number and confirm it with the code. A test number can only message up to **5 verified numbers**.
   - Send one message from the dashboard to your number to check it works.
3. **Token.** The token on the API Setup page expires within about a day. Use it to try things out, then create a permanent one:
   1. Open [Business settings](https://business.facebook.com/settings), then **Users → System users → Add**. Create a user with the **Admin** role.
   2. Click **Assign assets**. Give it your app (full control) and your WhatsApp account (full control).
   3. Click **Generate new token**, pick your app, set expiry to **Never**, and tick `whatsapp_business_messaging` and `whatsapp_business_management`.
   4. Put the token in `WA_TOKEN`.
4. **App secret.** Go to **App settings → Basic → App secret → Show** and copy it into `WA_APP_SECRET`.
5. **Verify token.** Make up any random string and put it in `WA_VERIFY_TOKEN`, e.g. `openssl rand -hex 16`.
6. Deploy the server first (see below), because Meta checks the webhook URL as soon as you save it.
7. **Webhook.** Go to **WhatsApp → Configuration → Webhook → Edit**:
   - Callback URL: `https://YOUR_DOMAIN/webhook`
   - Verify token: the same value as `WA_VERIFY_TOKEN`
   - Click **Verify and save**, then under **Webhook fields** click **Manage** and subscribe to **`messages`**.
8. Send `/help` to the test number from your phone.

If the webhook verifies but no messages arrive, subscribe the app to your WhatsApp Business Account once:
`curl -X POST "https://graph.facebook.com/v25.0/<WABA_ID>/subscribed_apps" -H "Authorization: Bearer $WA_TOKEN"`. The WABA ID is shown on the API Setup page.

**No message templates are needed.** The bot only ever replies to a message you just sent, so it is always inside WhatsApp's 24 hour window, where free-form text is allowed. The `Messenger` interface still includes `SendTemplate`, ready for scheduled messages later.

## ClickUp setup

1. In ClickUp, click your avatar, then **Settings → Apps → API Token → Generate**. Copy the `pk_…` token into `CLICKUP_TOKEN`.
2. Find your workspace (team) ID:
   ```bash
   curl -s -H "Authorization: pk_YOUR_TOKEN" https://api.clickup.com/api/v2/team
   ```
   Use `teams[].id` as `CLICKUP_TEAM_ID`. It's also the first number in ClickUp URLs: `app.clickup.com/<team_id>/…`.

The token acts as you, so the bot sees exactly what you can see in ClickUp.

## Deploying to your server

1. Point a DNS **A record** (e.g. `bot.yourdomain.com`) at the server's IP.
2. Install Docker with the compose plugin, then open only ports 80 and 443:
   ```bash
   sudo ufw allow OpenSSH && sudo ufw allow 80 && sudo ufw allow 443 && sudo ufw enable
   ```
3. Copy the repo to the server (`git clone` or `scp -r`), then:
   ```bash
   cd whatsapp-ops-bot
   cp .env.example .env && chmod 600 .env
   nano .env                    # set DOMAIN, WA_*, OWNER_WA_NUMBER, CLICKUP_*
   docker compose up -d --build
   docker compose logs -f app   # look for "server listening"
   curl https://bot.yourdomain.com/healthz   # → ok
   ```
   Caddy gets the HTTPS certificate on first start, which can take up to a minute.
4. Configure the Meta webhook (step 7 above) and send `/help`.

To update: `git pull && docker compose up -d --build`.

**Sharing the server with another site:** if something else already uses ports 80/443 (`Bind for 0.0.0.0:80 failed: port is already allocated`), skip the bundled Caddy. Add `COMPOSE_FILE=docker-compose.yml:docker-compose.shared-proxy.yml` to `.env` and run `docker compose up -d --build`. The app then listens on `127.0.0.1:8081` only. Point your existing proxy's `bot.yourdomain.com` at it, forwarding `/webhook` and `/healthz` and nothing else.

## Security

- **Signature check:** every `POST /webhook` must carry a valid `X-Hub-Signature-256` HMAC of the raw body, signed with your app secret and compared in constant time. Anything else gets 401.
- **Owner allowlist:** messages from any number not listed in `OWNER_WA_NUMBER` are silently ignored, with no reply and no ClickUp call.
- **Secrets:** they live only in `.env` (git-ignored and excluded from the image). Tokens, the app secret and message bodies are never logged at info level.
- **Exposure:** the app container isn't published to the host. Caddy exposes only `/webhook` and `/healthz` and returns 404 for everything else. Keep the firewall to 22, 80 and 443.
- **Container:** runs as a non-root user on distroless with no shell.

## Cost

Meta charges per delivered **template** message, by category and country. Free-form replies inside the 24 hour customer service window are currently free. Command replies are always free. The daily reminder is free when the owner messaged the bot in the last 24 hours. Otherwise it goes out as a utility template, a small per-message charge. Pricing changes, so check Meta's current rate card: <https://developers.facebook.com/docs/whatsapp/pricing>. ClickUp API access is included in every plan.

## Assumptions

- **Owners:** one or more owner numbers (`OWNER_WA_NUMBER`, comma-separated) sharing a single ClickUp workspace. Every owner sees the same data.
- **No database:** every command is read-only, so nothing is stored. Repeat deliveries from Meta are de-duplicated in memory for 24 hours. After a restart a repeated delivery could get a second reply, which does no harm.
- **Scope:** invoices and clients are out of scope for now. The only scheduled message is the daily `/due` reminder to the owners. Team members aren't messaged.
- **"Done":** a task is done when its status type is `closed` or `done`. "Finished this month" uses ClickUp's closed date, then its done date, then its last-updated date.
- **`/update` scope:** the calendar covers tasks due this month and next month. The team table and "overdue from earlier months" use every open task. Tasks with no due date appear in the team table and in `/<name>`, but not in the calendar.
- **Uploads:** `/uploads` lists earlier posts never marked as posted for the last 30 days only.
- **Time zone:** all dates use `TZ` (default `Europe/London`), and the zone database is built into the binary.
- **Long sections:** sections in `/<name>` and `/uploads` show up to 15 items, and "overdue from earlier months" in `/update` shows 10, then "+N more". Calendars list everything and split across messages if needed.
- **Name matching:** tries the full name, then the first name, then a prefix of any name word or email. Command words (`help`, `update`, `team`, …) take priority over member names.
- **Graph API version:** `v25.0` by default. Change `WA_GRAPH_VERSION` when Meta retires it.

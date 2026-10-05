#!/usr/bin/env bash
# Sends a signed fake WhatsApp message to a running bot, as Meta would.
#
#   ./scripts/simulate-webhook.sh "/update"
#   ./scripts/simulate-webhook.sh "/bob" http://localhost:8080/webhook
#   FROM=15550001111 ./scripts/simulate-webhook.sh "/help"   # a stranger: should be ignored
#
# Reads WA_APP_SECRET, OWNER_WA_NUMBER and WA_PHONE_NUMBER_ID from the
# environment or from .env in the repo root.
set -euo pipefail

TEXT="${1:-/help}"
URL="${2:-http://localhost:8080/webhook}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

env_value() {
  local key="$1"
  if [[ -n "${!key:-}" ]]; then
    printf '%s' "${!key}"
  elif [[ -f "$ROOT/.env" ]]; then
    grep -E "^${key}=" "$ROOT/.env" | tail -n1 | cut -d= -f2- | sed -e 's/^"//' -e 's/"$//'
  fi
}

SECRET="$(env_value WA_APP_SECRET)"
OWNER="$(env_value OWNER_WA_NUMBER)"
OWNER="${OWNER%%,*}"   # first owner when several are listed
OWNER="${OWNER// /}"
PHONE_ID="$(env_value WA_PHONE_NUMBER_ID)"
FROM="${FROM:-${OWNER#+}}"
: "${SECRET:?WA_APP_SECRET is not set}"
: "${FROM:?OWNER_WA_NUMBER is not set}"

# Escape the text for JSON.
ESCAPED="$(printf '%s' "$TEXT" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')"
WAMID="wamid.sim$(date +%s)$RANDOM"

BODY=$(cat <<EOF
{"object":"whatsapp_business_account","entry":[{"id":"0","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"display_phone_number":"0","phone_number_id":"${PHONE_ID}"},"contacts":[{"profile":{"name":"Sim"},"wa_id":"${FROM}"}],"messages":[{"from":"${FROM}","id":"${WAMID}","timestamp":"$(date +%s)","type":"text","text":{"body":"${ESCAPED}"}}]}}]}]}
EOF
)

SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" | sed 's/^.* //')"

echo "POST $URL  text=\"$TEXT\"  from=$FROM"
curl -sS -o /dev/null -w "HTTP %{http_code}\n" -X POST "$URL" \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: $SIG" \
  --data-binary "$BODY"

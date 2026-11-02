#!/usr/bin/env bash
# Minimal live check for a deployed gateway.
# Required: GATEWAY_URL, COMPANY_TOKEN
# Optional: OPENAI_MODEL, ANTHROPIC_MODEL, DEEPSEEK_MODEL
#
# The script never prints the company token or the upstream response body.
# It exits non-zero if any configured model does not return HTTP 200.
set -uo pipefail

: "${GATEWAY_URL:?set GATEWAY_URL, e.g. https://api.example.com}"
: "${COMPANY_TOKEN:?set COMPANY_TOKEN to a test company token}"

GATEWAY_URL="${GATEWAY_URL%/}"
TMP_FILE="$(mktemp)"
trap 'rm -f "$TMP_FILE"' EXIT
FAILED=0

error_code() {
	# Surface only the gateway's machine-readable error code.
	grep -o '"code":"[^"]*"' "$TMP_FILE" 2>/dev/null | head -n 1 | sed 's/.*:"//;s/"$//'
}

report() {
	local name="$1" model="$2" code="$3"
	if [ "$code" = "200" ]; then
		printf '%-12s %-30s HTTP %s PASS\n' "$name" "($model)" "$code"
	else
		local detail
		detail="$(error_code)"
		printf '%-12s %-30s HTTP %s FAIL %s\n' "$name" "($model)" "$code" "$detail"
		FAILED=1
	fi
}

check_chat() {
	local name="$1" model="$2"
	local code
	code="$(curl -sS --max-time 60 -o "$TMP_FILE" -w '%{http_code}' \
		"$GATEWAY_URL/v1/chat/completions" \
		-H "Authorization: Bearer $COMPANY_TOKEN" \
		-H 'Content-Type: application/json' \
		-d "{\"model\":\"${model}\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with OK\"}],\"max_tokens\":8,\"stream\":false}")" || code="000"
	report "$name" "$model" "$code"
}

check_messages() {
	local name="$1" model="$2"
	local code
	code="$(curl -sS --max-time 60 -o "$TMP_FILE" -w '%{http_code}' \
		"$GATEWAY_URL/v1/messages" \
		-H "x-api-key: $COMPANY_TOKEN" \
		-H 'anthropic-version: 2023-06-01' \
		-H 'Content-Type: application/json' \
		-d "{\"model\":\"${model}\",\"max_tokens\":8,\"messages\":[{\"role\":\"user\",\"content\":\"Reply with OK\"}]}")" || code="000"
	report "$name" "$model" "$code"
}

if [ -n "${OPENAI_MODEL:-}" ]; then
	check_chat openai "$OPENAI_MODEL"
else
	printf '%-12s %-30s SKIP\n' openai '(OPENAI_MODEL unset)'
fi

if [ -n "${DEEPSEEK_MODEL:-}" ]; then
	check_chat deepseek "$DEEPSEEK_MODEL"
else
	printf '%-12s %-30s SKIP\n' deepseek '(DEEPSEEK_MODEL unset)'
fi

if [ -n "${ANTHROPIC_MODEL:-}" ]; then
	check_messages anthropic "$ANTHROPIC_MODEL"
else
	printf '%-12s %-30s SKIP\n' anthropic '(ANTHROPIC_MODEL unset)'
fi

exit "$FAILED"

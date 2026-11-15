#!/usr/bin/env bash
# Smoke-test every M1 endpoint through HAProxy (127.0.0.1:8080). Prints one line per check.
set -euo pipefail

base=${BASE_URL:-http://127.0.0.1:8080}
fail=0
body=$(mktemp)
trap 'rm -f "$body"' EXIT

check() { # name expected_status curl-args...
  local name=$1 want=$2; shift 2
  local got
  got=$(curl -s -o "$body" -w '%{http_code}' "$@")
  if [ "$got" = "$want" ]; then echo "ok   $name -> $got $(head -c 160 "$body" | tr '\n' ' ')"; else echo "FAIL $name -> $got (want $want)"; fail=1; fi
}

key="smoke-$(date +%s%N)"
check "healthz"                   200 "$base/healthz"
check "readyz"                    200 "$base/readyz"
check "create order"              201 -X POST "$base/v1/orders" -H "Idempotency-Key: $key" -d '{"customer_id":"alice","amount_cents":1234}'
check "repeat key returns 200"    200 -X POST "$base/v1/orders" -H "Idempotency-Key: $key" -d '{"customer_id":"bob","amount_cents":9}'
check "missing Idempotency-Key"   400 -X POST "$base/v1/orders" -d '{"customer_id":"a","amount_cents":1}'
check "amount 0 rejected"         400 -X POST "$base/v1/orders" -H "Idempotency-Key: x$key" -d '{"customer_id":"a","amount_cents":0}'
id=$(curl -s -X POST "$base/v1/orders" -H "Idempotency-Key: $key" -d '{"customer_id":"a","amount_cents":1}' | jq -r .id)
check "get order"                 200 "$base/v1/orders/$id"
check "get unknown order"         404 "$base/v1/orders/00000000-0000-0000-0000-000000000000"
check "get bad id"                400 "$base/v1/orders/nope"
check "list limit 5"              200 "$base/v1/orders?limit=5"
check "post seq"                  201 -X POST "$base/v1/seq" -d "{\"writer_id\":\"smoke\",\"seq\":$(date +%s)}"
check "get seq"                   200 "$base/v1/seq/smoke"
check "debug blocked at edge"     403 -X POST "$base/debug/crash"
check "metrics blocked at edge"   403 "$base/metrics"
check "encoded debug blocked"     403 -X POST --path-as-is "$base/%64ebug/crash"
check "encoded metrics blocked"   403 --path-as-is "$base/%6Detrics"
check "double slash debug blocked" 403 -X POST --path-as-is "$base//debug/crash"
exit $fail

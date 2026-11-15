// Steady load: 70% reads, 30% writes at a constant arrival rate.
// Env: BASE_URL (default http://haproxy:8080), RPS (default 30), DURATION (default 5m).
// shortcut: the JSON summary file that `blast` parses arrives in M4; M1 reads the stdout summary.
import http from 'k6/http';
import { check } from 'k6';
import { Rate } from 'k6/metrics';

const base = __ENV.BASE_URL || 'http://haproxy:8080';
const rps = parseInt(__ENV.RPS || '30', 10);

const failed = new Rate('failed_requests');

export const options = {
  scenarios: {
    steady: {
      executor: 'constant-arrival-rate',
      rate: rps,
      timeUnit: '1s',
      duration: __ENV.DURATION || '5m',
      preAllocatedVUs: 10,
      maxVUs: 50,
    },
  },
  // PRD 8.4: p99 under 100 ms and zero failed requests with no fault active.
  thresholds: {
    http_req_duration: ['p(99)<100'],
    failed_requests: ['rate==0'],
  },
};

export function setup() {
  // One order so early reads have something to fetch.
  const r = http.post(`${base}/v1/orders`, JSON.stringify({ customer_id: 'seed', amount_cents: 100 }),
    { headers: { 'Content-Type': 'application/json', 'Idempotency-Key': 'k6-seed' } });
  return { id: r.json('id') };
}

export default function (data) {
  let res;
  const roll = Math.random();
  if (roll < 0.35) {
    res = http.get(`${base}/v1/orders?limit=10`, { tags: { kind: 'read' } });
  } else if (roll < 0.7) {
    res = http.get(`${base}/v1/orders/${data.id}`, { tags: { kind: 'read' } });
  } else {
    const key = `k6-${__VU}-${__ITER}-${Date.now()}`;
    res = http.post(`${base}/v1/orders`, JSON.stringify({ customer_id: `c${__VU}`, amount_cents: 1 + (__ITER % 1000) }),
      { headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key }, tags: { kind: 'write' } });
  }
  const ok = check(res, { 'status 2xx': (r) => r.status >= 200 && r.status < 300 });
  failed.add(!ok);
}

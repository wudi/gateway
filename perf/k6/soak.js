import http from "k6/http";
import { check, sleep } from "k6";
import { ROUTES, authHeaders, defaultHeaders, defaultThresholds, ADMIN_URL } from "./lib/helpers.js";

export const options = {
  stages: [
    { duration: "2m", target: 50 },   // Ramp up
    { duration: "30m", target: 50 },  // Sustained load
    { duration: "2m", target: 0 },    // Ramp down
  ],
  thresholds: {
    ...defaultThresholds,
    http_req_duration: ["p(95)<500", "p(99)<1500"],
  },
};

// Capture heap profile at test start
export function setup() {
  const res = http.get(`${ADMIN_URL}/health`);
  return { startTime: new Date().toISOString(), healthOk: res.status === 200 };
}

export default function () {
  const scenario = Math.random();

  if (scenario < 0.3) {
    const res = http.get(ROUTES.minimal, { headers: defaultHeaders });
    check(res, { "minimal: ok": (r) => r.status >= 200 && r.status < 300 });
  } else if (scenario < 0.6) {
    const res = http.get(ROUTES.standard, { headers: authHeaders });
    check(res, { "standard: ok": (r) => r.status >= 200 && r.status < 300 });
  } else if (scenario < 0.8) {
    const res = http.get(ROUTES.cached, { headers: defaultHeaders });
    check(res, { "cached: ok": (r) => r.status >= 200 && r.status < 300 });
  } else {
    const res = http.get(ROUTES.full, { headers: authHeaders });
    check(res, { "full: ok": (r) => r.status >= 200 && r.status < 300 });
  }

  sleep(0.1);
}

export function teardown(data) {
  console.log(`Soak test completed. Started at: ${data.startTime}`);
  console.log(`Initial health check: ${data.healthOk ? "OK" : "FAILED"}`);
}

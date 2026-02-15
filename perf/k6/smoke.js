import http from "k6/http";
import { check, sleep } from "k6";
import { ROUTES, authHeaders, defaultHeaders, checkOK } from "./lib/helpers.js";

export const options = {
  vus: 1,
  duration: "10s",
  thresholds: {
    http_req_failed: ["rate<0.01"],
    http_req_duration: ["p(95)<1000"],
  },
};

export default function () {
  // Minimal route (no auth)
  let res = http.get(ROUTES.minimal, { headers: defaultHeaders });
  check(res, { "minimal: status 200": (r) => checkOK(r) });

  // Standard route (auth required)
  res = http.get(ROUTES.standard, { headers: authHeaders });
  check(res, { "standard: status 200": (r) => checkOK(r) });

  // Cached route
  res = http.get(ROUTES.cached, { headers: defaultHeaders });
  check(res, { "cached: status 200": (r) => checkOK(r) });

  // Rate limited route
  res = http.get(ROUTES.ratelimited, { headers: defaultHeaders });
  check(res, {
    "ratelimited: status 200 or 429": (r) => r.status === 200 || r.status === 429,
  });

  // Full route (auth required)
  res = http.get(ROUTES.full, { headers: authHeaders });
  check(res, { "full: status 200": (r) => checkOK(r) });

  // Health route
  res = http.get(ROUTES.health, { headers: defaultHeaders });
  check(res, { "health: status 200": (r) => checkOK(r) });

  sleep(0.5);
}

import http from "k6/http";
import { check, sleep } from "k6";
import { Trend } from "k6/metrics";
import { ROUTES, authHeaders, defaultHeaders, defaultThresholds, checkOK } from "./lib/helpers.js";

// Per-route latency trends
const minimalLatency = new Trend("route_minimal_duration", true);
const standardLatency = new Trend("route_standard_duration", true);
const cachedLatency = new Trend("route_cached_duration", true);
const fullLatency = new Trend("route_full_duration", true);

export const options = {
  stages: [
    { duration: "2m", target: 20 },   // Ramp up
    { duration: "5m", target: 50 },   // Hold at 50
    { duration: "3m", target: 100 },  // Ramp to peak
    { duration: "2m", target: 100 },  // Hold at peak
    { duration: "2m", target: 0 },    // Ramp down
  ],
  thresholds: {
    ...defaultThresholds,
    route_minimal_duration: ["p(95)<200"],
    route_standard_duration: ["p(95)<300"],
    route_cached_duration: ["p(95)<100"],
    route_full_duration: ["p(95)<500"],
  },
};

export default function () {
  // Minimal route - baseline
  let res = http.get(ROUTES.minimal, { headers: defaultHeaders });
  check(res, { "minimal: ok": (r) => checkOK(r) });
  minimalLatency.add(res.timings.duration);

  // Standard route - auth + rate limit
  res = http.get(ROUTES.standard, { headers: authHeaders });
  check(res, { "standard: ok": (r) => checkOK(r) });
  standardLatency.add(res.timings.duration);

  // Cached route - should have high hit rate
  res = http.get(ROUTES.cached, { headers: defaultHeaders });
  check(res, { "cached: ok": (r) => checkOK(r) });
  cachedLatency.add(res.timings.duration);

  // Full route - all features
  res = http.get(ROUTES.full, { headers: authHeaders });
  check(res, { "full: ok": (r) => checkOK(r) });
  fullLatency.add(res.timings.duration);

  sleep(0.1);
}

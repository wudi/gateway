import http from "k6/http";
import { check, sleep } from "k6";
import { stressThresholds, ROUTES, authHeaders, defaultHeaders } from "./lib/helpers.js";

export const options = {
  stages: [
    { duration: "1m", target: 50 },    // Warm up
    { duration: "2m", target: 200 },   // Ramp up
    { duration: "2m", target: 500 },   // Push harder
    { duration: "2m", target: 1000 },  // High load
    { duration: "2m", target: 1500 },  // Breaking point
    { duration: "2m", target: 0 },     // Recovery
  ],
  thresholds: stressThresholds,
};

export default function () {
  // Distribute load across routes
  const scenario = Math.random();

  if (scenario < 0.4) {
    // 40% minimal (baseline proxy overhead)
    const res = http.get(ROUTES.minimal, { headers: defaultHeaders });
    check(res, { "minimal: responding": (r) => r.status !== 0 });
  } else if (scenario < 0.7) {
    // 30% standard (auth + rate limit)
    const res = http.get(ROUTES.standard, { headers: authHeaders });
    check(res, { "standard: responding": (r) => r.status !== 0 });
  } else if (scenario < 0.9) {
    // 20% cached
    const res = http.get(ROUTES.cached, { headers: defaultHeaders });
    check(res, { "cached: responding": (r) => r.status !== 0 });
  } else {
    // 10% full stack
    const res = http.get(ROUTES.full, { headers: authHeaders });
    check(res, { "full: responding": (r) => r.status !== 0 });
  }

  sleep(0.05);
}

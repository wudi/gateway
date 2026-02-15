import http from "k6/http";
import { check, sleep } from "k6";
import { ROUTES, authHeaders, defaultHeaders, stressThresholds } from "./lib/helpers.js";

export const options = {
  stages: [
    { duration: "30s", target: 10 },   // Normal load
    { duration: "10s", target: 500 },  // Spike!
    { duration: "1m", target: 500 },   // Hold spike
    { duration: "10s", target: 10 },   // Drop back
    { duration: "1m", target: 10 },    // Recovery
  ],
  thresholds: stressThresholds,
};

export default function () {
  const scenario = Math.random();

  if (scenario < 0.5) {
    const res = http.get(ROUTES.minimal, { headers: defaultHeaders });
    check(res, { "minimal: responding": (r) => r.status !== 0 });
  } else {
    const res = http.get(ROUTES.standard, { headers: authHeaders });
    check(res, { "standard: responding": (r) => r.status !== 0 });
  }

  sleep(0.05);
}

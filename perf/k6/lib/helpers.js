// Shared constants and utilities for k6 load tests

export const BASE_URL = __ENV.BASE_URL || "http://gateway:8080";
export const ADMIN_URL = __ENV.ADMIN_URL || "http://gateway:8081";
export const API_KEY = __ENV.API_KEY || "perf-test-key";

// Standard headers for authenticated requests
export const authHeaders = {
  "X-API-Key": API_KEY,
  "Content-Type": "application/json",
};

// Standard headers for unauthenticated requests
export const defaultHeaders = {
  "Content-Type": "application/json",
};

// Routes
export const ROUTES = {
  minimal: `${BASE_URL}/perf/minimal`,
  standard: `${BASE_URL}/perf/standard`,
  cached: `${BASE_URL}/perf/cached`,
  ratelimited: `${BASE_URL}/perf/ratelimited`,
  full: `${BASE_URL}/perf/full`,
  health: `${BASE_URL}/perf/health`,
};

// Default thresholds
export const defaultThresholds = {
  http_req_failed: ["rate<0.01"],
  http_req_duration: ["p(95)<500", "p(99)<1000"],
};

// Relaxed thresholds for stress tests
export const stressThresholds = {
  http_req_failed: ["rate<0.10"],
  http_req_duration: ["p(95)<2000", "p(99)<5000"],
};

// Check that a response is successful (2xx)
export function checkOK(res) {
  return res.status >= 200 && res.status < 300;
}

// Check that a response is rate limited (429)
export function checkRateLimited(res) {
  return res.status === 429;
}

package runway

// Well-known middleware names that can be used as After/Before anchors
// in MiddlewareSlot and GlobalMiddlewareSlot. Grouped by pipeline phase.
const (
	// --- Observability ---
	MWMetrics        = "metrics"
	MWSLO            = "slo"
	MWCanaryObserver = "canary_observer"

	// --- Ingress ---
	MWIPFilter    = "ip_filter"
	MWGeo         = "geo"
	MWMaintenance = "maintenance"
	MWBotDetect   = "bot_detection"
	MWIPBlocklist = "ip_blocklist"
	MWClientMTLS  = "client_mtls"

	// --- CORS & Headers ---
	MWCORS            = "cors"
	MWVarContext       = "var_context"
	MWSecurityHeaders  = "security_headers"
	MWCDNHeaders       = "cdn_headers"
	MWErrorPages       = "error_pages"
	MWAccessLog        = "access_log"
	MWAuditLog         = "audit_log"
	MWVersioning       = "versioning"
	MWDeprecation      = "deprecation"
	MWTimeout          = "timeout"

	// --- Traffic Control ---
	MWRateLimit    = "rate_limit"
	MWSpikeArrest  = "spike_arrest"
	MWQuota        = "quota"
	MWThrottle     = "throttle"
	MWRequestQueue = "request_queue"

	// --- Authentication ---
	MWAuth          = "auth"
	MWTokenRevoke   = "token_revocation"
	MWTokenExchange = "token_exchange"
	MWClaimsProp    = "claims_propagation"
	MWExtAuth       = "ext_auth"
	MWOPA           = "opa"
	MWNonce         = "nonce"
	MWCSRF          = "csrf"
	MWInboundSigning = "inbound_signing"
	MWIdempotency   = "idempotency"
	MWDedup         = "dedup"
	MWPriority      = "priority"
	MWBaggage       = "baggage"
	MWTenant        = "tenant"
	MWConsumerGroup = "consumer_group"
	MWCostTrack     = "cost_track"

	// --- Request Processing ---
	MWRequestRules   = "request_rules"
	MWWAF            = "waf"
	MWFaultInjection = "fault_injection"
	MWTrafficReplay  = "traffic_replay"
	MWMock           = "mock"
	MWLuaRequest     = "lua_request"
	MWWasmRequest    = "wasm_request"

	// --- Body ---
	MWBodyLimit          = "body_limit"
	MWConnect            = "connect"
	MWRequestDecompress  = "request_decompress"
	MWBandwidth          = "bandwidth"
	MWFieldEncrypt       = "field_encrypt"
	MWValidation         = "validation"
	MWOpenAPIRequest     = "openapi_request"
	MWGraphQL            = "graphql"
	MWGraphQLSubscription = "graphql_subscription"

	// --- Protocol ---
	MWWebSocket = "websocket"
	MWSSE       = "sse"

	// --- Caching ---
	MWCache    = "cache"
	MWCoalesce = "coalesce"

	// --- Resilience ---
	MWCircuitBreaker      = "circuit_breaker"
	MWOutlierDetection    = "outlier_detection"
	MWAdaptiveConcurrency = "adaptive_concurrency"
	MWBackpressure        = "backpressure"
	MWProxyRateLimit      = "proxy_rate_limit"
	MWStreaming            = "streaming"

	// --- Response Processing ---
	MWCompression    = "compression"
	MWResponseLimit  = "response_limit"
	MWETag           = "etag"
	MWResponseRules  = "response_rules"
	MWMirror         = "mirror"
	MWTrafficGroup   = "traffic_group"
	MWSessionAffinity = "session_affinity"

	// --- Transform ---
	MWRequestTransform     = "request_transform"
	MWBodyGen              = "body_gen"
	MWModifiers            = "modifiers"
	MWParamForward         = "param_forward"
	MWBackendAuth          = "backend_auth"
	MWBackendSigning       = "backend_signing"
	MWResponseTransform    = "response_transform"
	MWWasmResponse         = "wasm_response"
	MWLuaResponse          = "lua_response"
	MWJMESPath             = "jmespath"
	MWStatusMap            = "status_map"
	MWContentReplacer      = "content_replacer"
	MWPIIRedact            = "pii_redact"
	MWFieldReplacer        = "field_replacer"
	MWRespBodyGen          = "resp_body_gen"
	MWErrorHandling        = "error_handling"
	MWContentNeg           = "content_neg"
	MWResponseSigning      = "response_signing"

	// --- Global handler chain ---
	MWGlobalRecovery        = "recovery"
	MWGlobalRealIP          = "real_ip"
	MWGlobalHTTPSRedirect   = "https_redirect"
	MWGlobalAllowedHosts    = "allowed_hosts"
	MWGlobalRequestID       = "request_id"
	MWGlobalLoadShed        = "load_shed"
	MWGlobalServiceRateLimit = "service_rate_limit"
	MWGlobalAltSvc          = "alt_svc"
	MWGlobalMTLS            = "mtls"
	MWGlobalTracing         = "tracing"
	MWGlobalLogging         = "logging"
)

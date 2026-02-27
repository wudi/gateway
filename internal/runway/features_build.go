package runway

import (
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware/aicrawl"
	"github.com/wudi/runway/internal/middleware/auditlog"
	"github.com/wudi/runway/internal/middleware/baggage"
	"github.com/wudi/runway/internal/middleware/botdetect"
	"github.com/wudi/runway/internal/middleware/cdnheaders"
	"github.com/wudi/runway/internal/middleware/clientmtls"
	"github.com/wudi/runway/internal/middleware/csrf"
	"github.com/wudi/runway/internal/middleware/decompress"
	"github.com/wudi/runway/internal/middleware/deprecation"
	"github.com/wudi/runway/internal/middleware/edgecacherules"
	"github.com/wudi/runway/internal/middleware/geo"
	"github.com/wudi/runway/internal/middleware/idempotency"
	"github.com/wudi/runway/internal/middleware/inboundsigning"
	"github.com/wudi/runway/internal/middleware/ipblocklist"
	"github.com/wudi/runway/internal/middleware/maintenance"
	"github.com/wudi/runway/internal/middleware/nonce"
	openapivalidation "github.com/wudi/runway/internal/middleware/openapi"
	"github.com/wudi/runway/internal/middleware/requestqueue"
	"github.com/wudi/runway/internal/middleware/responselimit"
	"github.com/wudi/runway/internal/middleware/securityheaders"
	"github.com/wudi/runway/internal/middleware/signing"
	"github.com/wudi/runway/internal/middleware/spikearrest"
	"github.com/wudi/runway/internal/trafficshape"
)

// buildFeatures returns the per-route Feature list shared by both New() and
// buildState(). This single definition eliminates feature-list divergence
// between initial startup and config reload.
//
// Features that need Runway-level fields (routeProxies, tracer, etc.) are NOT
// included here; they go in Runway.buildAdminFeatures() instead.
func buildFeatures(rm *routeManagers, cfg *config.Config, redisClient *redis.Client) []Feature {
	return []Feature{
		// ---- Simple per-route features (enabledFeature) ----

		enabledFeature("ip_filter", "", rm.ipFilters, func(rc config.RouteConfig) config.IPFilterConfig { return rc.IPFilter }),
		enabledFeature("cors", "", rm.corsHandlers, func(rc config.RouteConfig) config.CORSConfig { return rc.CORS }),
		enabledFeature("compression", "/compression", rm.compressors, func(rc config.RouteConfig) config.CompressionConfig { return rc.Compression }),
		enabledFeature("validation", "", rm.validators, func(rc config.RouteConfig) config.ValidationConfig { return rc.Validation }),
		enabledFeature("waf", "/waf", rm.wafHandlers, func(rc config.RouteConfig) config.WAFConfig { return rc.WAF }),
		enabledFeature("graphql", "/graphql", rm.graphqlParsers, func(rc config.RouteConfig) config.GraphQLConfig { return rc.GraphQL }),
		enabledFeature("coalesce", "/coalesce", rm.coalescers, func(rc config.RouteConfig) config.CoalesceConfig { return rc.Coalesce }),
		enabledFeature("versioning", "/versioning", rm.versioners, func(rc config.RouteConfig) config.VersioningConfig { return rc.Versioning }),
		enabledFeature("proxy_rate_limit", "/proxy-rate-limits", rm.proxyRateLimiters, func(rc config.RouteConfig) config.ProxyRateLimitConfig { return rc.ProxyRateLimit }),
		enabledFeature("claims_propagation", "/claims-propagation", rm.claimsPropagators, func(rc config.RouteConfig) config.ClaimsPropagationConfig { return rc.ClaimsPropagation }),
		enabledFeature("token_exchange", "/token-exchange", rm.tokenExchangers, func(rc config.RouteConfig) config.TokenExchangeConfig { return rc.TokenExchange }),
		enabledFeature("backend_auth", "/backend-auth", rm.backendAuths, func(rc config.RouteConfig) config.BackendAuthConfig { return rc.BackendAuth }),
		enabledFeature("fastcgi", "/fastcgi", rm.fastcgiHandlers, func(rc config.RouteConfig) config.FastCGIConfig { return rc.FastCGI }),
		enabledFeature("ai", "/ai", rm.aiHandlers, func(rc config.RouteConfig) config.AIConfig { return rc.AI }),
		enabledFeature("body_generator", "/body-generator", rm.bodyGenerators, func(rc config.RouteConfig) config.BodyGeneratorConfig { return rc.BodyGenerator }),
		enabledFeature("response_body_generator", "/response-body-generator", rm.respBodyGenerators, func(rc config.RouteConfig) config.ResponseBodyGeneratorConfig { return rc.ResponseBodyGenerator }),
		enabledFeature("param_forwarding", "/param-forwarding", rm.paramForwarders, func(rc config.RouteConfig) config.ParamForwardingConfig { return rc.ParamForwarding }),
		enabledFeature("pii_redaction", "/pii-redaction", rm.piiRedactors, func(rc config.RouteConfig) config.PIIRedactionConfig { return rc.PIIRedaction }),
		enabledFeature("field_encryption", "/field-encryption", rm.fieldEncryptors, func(rc config.RouteConfig) config.FieldEncryptionConfig { return rc.FieldEncryption }),
		enabledFeature("jmespath", "/jmespath", rm.jmespathHandlers, func(rc config.RouteConfig) config.JMESPathConfig { return rc.JMESPath }),
		enabledFeature("field_replacer", "/field-replacer", rm.fieldReplacers, func(rc config.RouteConfig) config.FieldReplacerConfig { return rc.FieldReplacer }),
		enabledFeature("lua", "/lua", rm.luaScripters, func(rc config.RouteConfig) config.LuaConfig { return rc.Lua }),
		enabledFeature("traffic_replay", "/traffic-replay", rm.trafficReplay, func(rc config.RouteConfig) config.TrafficReplayConfig { return rc.TrafficReplay }),
		enabledFeature("opa", "/opa", rm.opaEnforcers, func(rc config.RouteConfig) config.OPAConfig { return rc.OPA }),
		enabledFeature("response_signing", "/response-signing", rm.responseSigners, func(rc config.RouteConfig) config.ResponseSigningConfig { return rc.ResponseSigning }),
		enabledFeature("request_cost", "/request-cost", rm.costTrackers, func(rc config.RouteConfig) config.RequestCostConfig { return rc.RequestCost }),
		enabledFeature("graphql_subscriptions", "/graphql-subscriptions", rm.graphqlSubs, func(rc config.RouteConfig) config.GraphQLSubscriptionConfig { return rc.GraphQL.Subscriptions }),
		enabledFeature("connect", "/connect", rm.connectHandlers, func(rc config.RouteConfig) config.ConnectConfig { return rc.Connect }),
		enabledFeature("slo", "/slo", rm.sloTrackers, func(rc config.RouteConfig) config.SLOConfig { return rc.SLO }),
		enabledFeature("etag", "/etag", rm.etagHandlers, func(rc config.RouteConfig) config.ETagConfig { return rc.ETag }),
		enabledFeature("streaming", "/streaming", rm.streamHandlers, func(rc config.RouteConfig) config.StreamingConfig { return rc.Streaming }),
		enabledFeature("ext_auth", "/ext-auth", rm.extAuths, func(rc config.RouteConfig) config.ExtAuthConfig { return rc.ExtAuth }),
		enabledFeature("sse", "/sse", rm.sseHandlers, func(rc config.RouteConfig) config.SSEConfig { return rc.SSE }),
		enabledFeature("request_dedup", "/request-dedup", rm.dedupHandlers, func(rc config.RouteConfig) config.RequestDedupConfig { return rc.RequestDedup }),
		enabledFeature("quota", "/quotas", rm.quotaEnforcers, func(rc config.RouteConfig) config.QuotaConfig { return rc.Quota }),

		// Simple features with non-standard enabled checks (keep featureFor)
		featureFor("content_replacer", "/content-replacer", rm.contentReplacers, func(rc config.RouteConfig) (config.ContentReplacerConfig, bool) {
			return rc.ContentReplacer, rc.ContentReplacer.Enabled && len(rc.ContentReplacer.Replacements) > 0
		}),
		featureFor("error_handling", "/error-handling", rm.errorHandlers, func(rc config.RouteConfig) (config.ErrorHandlingConfig, bool) {
			return rc.ErrorHandling, rc.ErrorHandling.Mode != "" && rc.ErrorHandling.Mode != "default"
		}),
		featureFor("status_mapping", "/status-mapping", rm.statusMappers, func(rc config.RouteConfig) (map[int]int, bool) {
			return rc.StatusMapping.Mappings, rc.StatusMapping.Enabled && len(rc.StatusMapping.Mappings) > 0
		}),
		featureFor("backend_encoding", "/backend-encoding", rm.backendEncoders, func(rc config.RouteConfig) (config.BackendEncodingConfig, bool) {
			return rc.BackendEncoding, rc.BackendEncoding.Encoding != ""
		}),
		featureFor("rules", "", rm.routeRules, func(rc config.RouteConfig) (config.RulesConfig, bool) {
			return rc.Rules, len(rc.Rules.Request) > 0 || len(rc.Rules.Response) > 0
		}),
		featureFor("modifiers", "/modifiers", rm.modifierChains, func(rc config.RouteConfig) ([]config.ModifierConfig, bool) {
			return rc.Modifiers, len(rc.Modifiers) > 0
		}),
		featureFor("wasm", "/wasm-plugins", rm.wasmPlugins, func(rc config.RouteConfig) ([]config.WasmPluginConfig, bool) {
			return rc.WasmPlugins, len(rc.WasmPlugins) > 0
		}),
		featureFor("timeout", "/timeouts", rm.timeoutConfigs, func(rc config.RouteConfig) (config.TimeoutConfig, bool) {
			return rc.TimeoutPolicy, rc.TimeoutPolicy.IsActive()
		}),

		// ---- Merge features: per-route config merged with global defaults (enabledMerge) ----

		enabledMerge("throttle", "", rm.throttlers,
			func(rc config.RouteConfig) config.ThrottleConfig { return rc.TrafficShaping.Throttle },
			func() config.ThrottleConfig { return cfg.TrafficShaping.Throttle },
			trafficshape.MergeThrottleConfig),
		enabledMerge("bandwidth", "", rm.bandwidthLimiters,
			func(rc config.RouteConfig) config.BandwidthConfig { return rc.TrafficShaping.Bandwidth },
			func() config.BandwidthConfig { return cfg.TrafficShaping.Bandwidth },
			trafficshape.MergeBandwidthConfig),
		enabledMerge("fault_injection", "", rm.faultInjectors,
			func(rc config.RouteConfig) config.FaultInjectionConfig { return rc.TrafficShaping.FaultInjection },
			func() config.FaultInjectionConfig { return cfg.TrafficShaping.FaultInjection },
			trafficshape.MergeFaultInjectionConfig),
		enabledMerge("request_queue", "/request-queues", rm.requestQueues,
			func(rc config.RouteConfig) config.RequestQueueConfig { return rc.TrafficShaping.RequestQueue },
			func() config.RequestQueueConfig { return cfg.TrafficShaping.RequestQueue },
			requestqueue.MergeRequestQueueConfig),
		enabledMerge("request_decompression", "/decompression", rm.decompressors,
			func(rc config.RouteConfig) config.RequestDecompressionConfig { return rc.RequestDecompression },
			func() config.RequestDecompressionConfig { return cfg.RequestDecompression },
			decompress.MergeDecompressionConfig),
		enabledMerge("response_limit", "/response-limits", rm.responseLimiters,
			func(rc config.RouteConfig) config.ResponseLimitConfig { return rc.ResponseLimit },
			func() config.ResponseLimitConfig { return cfg.ResponseLimit },
			responselimit.MergeResponseLimitConfig),
		enabledMerge("security_headers", "/security-headers", rm.securityHeaders,
			func(rc config.RouteConfig) config.SecurityHeadersConfig { return rc.SecurityHeaders },
			func() config.SecurityHeadersConfig { return cfg.SecurityHeaders },
			securityheaders.MergeSecurityHeadersConfig),
		enabledMerge("maintenance", "/maintenance", rm.maintenanceHandlers,
			func(rc config.RouteConfig) config.MaintenanceConfig { return rc.Maintenance },
			func() config.MaintenanceConfig { return cfg.Maintenance },
			maintenance.MergeMaintenanceConfig),
		enabledMerge("bot_detection", "/bot-detection", rm.botDetectors,
			func(rc config.RouteConfig) config.BotDetectionConfig { return rc.BotDetection },
			func() config.BotDetectionConfig { return cfg.BotDetection },
			botdetect.MergeBotDetectionConfig),
		enabledMerge("ai_crawl_control", "/ai-crawl-control", rm.aiCrawlControllers,
			func(rc config.RouteConfig) config.AICrawlConfig { return rc.AICrawlControl },
			func() config.AICrawlConfig { return cfg.AICrawlControl },
			aicrawl.MergeAICrawlConfig),
		enabledMerge("spike_arrest", "/spike-arrest", rm.spikeArresters,
			func(rc config.RouteConfig) config.SpikeArrestConfig { return rc.SpikeArrest },
			func() config.SpikeArrestConfig { return cfg.SpikeArrest },
			spikearrest.MergeSpikeArrestConfig),
		enabledMerge("client_mtls", "/client-mtls", rm.clientMTLSVerifiers,
			func(rc config.RouteConfig) config.ClientMTLSConfig { return rc.ClientMTLS },
			func() config.ClientMTLSConfig { return cfg.ClientMTLS },
			clientmtls.MergeClientMTLSConfig),
		enabledMerge("backend_signing", "/signing", rm.backendSigners,
			func(rc config.RouteConfig) config.BackendSigningConfig { return rc.BackendSigning },
			func() config.BackendSigningConfig { return cfg.BackendSigning },
			signing.MergeSigningConfig),
		enabledMerge("inbound_signing", "/inbound-signing", rm.inboundVerifiers,
			func(rc config.RouteConfig) config.InboundSigningConfig { return rc.InboundSigning },
			func() config.InboundSigningConfig { return cfg.InboundSigning },
			inboundsigning.MergeInboundSigningConfig),
		enabledMerge("deprecation", "/deprecation", rm.deprecationHandlers,
			func(rc config.RouteConfig) config.DeprecationConfig { return rc.Deprecation },
			func() config.DeprecationConfig { return cfg.Deprecation },
			deprecation.MergeDeprecationConfig),
		enabledMerge("baggage", "/baggage", rm.baggagePropagators,
			func(rc config.RouteConfig) config.BaggageConfig { return rc.Baggage },
			func() config.BaggageConfig { return cfg.Baggage },
			baggage.MergeBaggageConfig),
		enabledMerge("audit_log", "/audit-log", rm.auditLoggers,
			func(rc config.RouteConfig) config.AuditLogConfig { return rc.AuditLog },
			func() config.AuditLogConfig { return cfg.AuditLog },
			auditlog.MergeAuditLogConfig),
		enabledMerge("nonce", "/nonces", rm.nonceCheckers,
			func(rc config.RouteConfig) config.NonceConfig { return rc.Nonce },
			func() config.NonceConfig { return cfg.Nonce },
			nonce.MergeNonceConfig),
		enabledMerge("csrf", "/csrf", rm.csrfProtectors,
			func(rc config.RouteConfig) config.CSRFConfig { return rc.CSRF },
			func() config.CSRFConfig { return cfg.CSRF },
			csrf.MergeCSRFConfig),
		enabledMerge("idempotency", "/idempotency", rm.idempotencyHandlers,
			func(rc config.RouteConfig) config.IdempotencyConfig { return rc.Idempotency },
			func() config.IdempotencyConfig { return cfg.Idempotency },
			idempotency.MergeIdempotencyConfig),
		enabledMerge("ip_blocklist", "/ip-blocklist", rm.ipBlocklists,
			func(rc config.RouteConfig) config.IPBlocklistConfig { return rc.IPBlocklist },
			func() config.IPBlocklistConfig { return cfg.IPBlocklist },
			ipblocklist.MergeIPBlocklistConfig),

		// ---- Custom features: unique logic that can't be generalized ----

		newFeature("circuit_breaker", "/circuit-breakers", func(id string, rc config.RouteConfig) error {
			if rc.CircuitBreaker.Enabled {
				if rc.CircuitBreaker.Mode == "distributed" && redisClient != nil {
					rm.circuitBreakers.AddRouteDistributed(id, rc.CircuitBreaker, redisClient)
				} else {
					rm.circuitBreakers.AddRoute(id, rc.CircuitBreaker)
				}
			}
			return nil
		}, rm.circuitBreakers.RouteIDs, func() any { return rm.circuitBreakers.Snapshots() }),

		newFeature("cache", "/cache", func(id string, rc config.RouteConfig) error {
			if rc.Cache.Enabled {
				rm.caches.AddRoute(id, rc.Cache)
			}
			return nil
		}, rm.caches.RouteIDs, func() any { return rm.caches.Stats() }),

		featureForWithStats("mirror", "/mirrors", rm.mirrors,
			func(rc config.RouteConfig) (config.MirrorConfig, bool) { return rc.Mirror, rc.Mirror.Enabled },
			func() any { return rm.mirrors.Stats() }),

		featureForWithStats("access_log", "/access-log", rm.accessLogConfigs, func(rc config.RouteConfig) (config.AccessLogConfig, bool) {
			al := rc.AccessLog
			return al, al.Enabled != nil || al.Format != "" ||
				len(al.HeadersInclude) > 0 || len(al.HeadersExclude) > 0 ||
				al.Body.Enabled ||
				al.Conditions.SampleRate > 0 || len(al.Conditions.StatusCodes) > 0 ||
				len(al.Conditions.Methods) > 0
		}, func() any { return rm.accessLogConfigs.Stats() }),

		featureForWithStats("openapi", "/openapi", rm.openapiValidators, func(rc config.RouteConfig) (config.OpenAPIRouteConfig, bool) {
			return rc.OpenAPI, rc.OpenAPI.SpecFile != "" || rc.OpenAPI.SpecID != ""
		}, func() any { return rm.openapiValidators.Stats() }),

		newFeature("mock_response", "/mock-responses", func(id string, rc config.RouteConfig) error {
			if rc.MockResponse.Enabled {
				if rc.MockResponse.FromSpec {
					specFile := rc.OpenAPI.SpecFile
					if specFile != "" {
						doc, err := openapivalidation.LoadSpec(specFile)
						if err != nil {
							return fmt.Errorf("mock from_spec: %w", err)
						}
						rm.mockHandlers.AddSpecRoute(id, rc.MockResponse, doc)
						return nil
					}
				}
				rm.mockHandlers.AddRoute(id, rc.MockResponse)
			}
			return nil
		}, rm.mockHandlers.RouteIDs, func() any { return rm.mockHandlers.Stats() }),

		newFeature("static_files", "/static-files", func(id string, rc config.RouteConfig) error {
			if rc.Static.Enabled {
				return rm.staticFiles.AddRoute(id, rc.Static.Root, rc.Static.Index, rc.Static.Browse, rc.Static.CacheControl)
			}
			return nil
		}, rm.staticFiles.RouteIDs, func() any { return rm.staticFiles.Stats() }),

		newFeature("content_negotiation", "/content-negotiation", func(id string, rc config.RouteConfig) error {
			if rc.ContentNegotiation.Enabled || rc.OutputEncoding != "" {
				cnCfg := rc.ContentNegotiation
				if !cnCfg.Enabled && rc.OutputEncoding != "" {
					cnCfg.Enabled = true
					cnCfg.Supported = []string{"json", "xml", "yaml"}
					cnCfg.Default = "json"
				}
				return rm.contentNegotiators.AddRoute(id, cnCfg, rc.OutputEncoding)
			}
			return nil
		}, rm.contentNegotiators.RouteIDs, func() any { return rm.contentNegotiators.Stats() }),

		newFeature("adaptive_concurrency", "/adaptive-concurrency", func(id string, rc config.RouteConfig) error {
			ac := rc.TrafficShaping.AdaptiveConcurrency
			if ac.Enabled {
				return rm.adaptiveLimiters.AddRoute(id, trafficshape.MergeAdaptiveConcurrencyConfig(ac, cfg.TrafficShaping.AdaptiveConcurrency))
			} else if cfg.TrafficShaping.AdaptiveConcurrency.Enabled {
				return rm.adaptiveLimiters.AddRoute(id, cfg.TrafficShaping.AdaptiveConcurrency)
			}
			return nil
		}, rm.adaptiveLimiters.RouteIDs, func() any { return rm.adaptiveLimiters.Stats() }),

		newFeature("priority", "", func(id string, rc config.RouteConfig) error {
			pc := rc.TrafficShaping.Priority
			if pc.Enabled {
				rm.priorityConfigs.AddRoute(id, trafficshape.MergePriorityConfig(pc, cfg.TrafficShaping.Priority))
			} else if cfg.TrafficShaping.Priority.Enabled {
				rm.priorityConfigs.AddRoute(id, cfg.TrafficShaping.Priority)
			}
			return nil
		}, rm.priorityConfigs.RouteIDs, nil),

		newFeature("error_pages", "/error-pages", func(id string, rc config.RouteConfig) error {
			if cfg.ErrorPages.IsActive() || rc.ErrorPages.IsActive() {
				return rm.errorPages.AddRoute(id, cfg.ErrorPages, rc.ErrorPages)
			}
			return nil
		}, rm.errorPages.RouteIDs, func() any { return rm.errorPages.Stats() }),

		newFeature("geo", "/geo", func(id string, rc config.RouteConfig) error {
			if rm.geoProvider == nil {
				return nil
			}
			if rc.Geo.Enabled {
				return rm.geoFilters.AddRoute(id, geo.MergeGeoConfig(rc.Geo, cfg.Geo))
			}
			if cfg.Geo.Enabled {
				return rm.geoFilters.AddRoute(id, cfg.Geo)
			}
			return nil
		}, rm.geoFilters.RouteIDs, func() any { return rm.geoFilters.Stats() }),

		// Always-merge features: merge first, then check if result is active
		newFeature("cdn_cache_headers", "/cdn-cache-headers", func(id string, rc config.RouteConfig) error {
			merged := cdnheaders.MergeCDNCacheConfig(rc.CDNCacheHeaders, cfg.CDNCacheHeaders)
			if merged.Enabled {
				return rm.cdnHeaders.AddRoute(id, merged)
			}
			return nil
		}, rm.cdnHeaders.RouteIDs, func() any { return rm.cdnHeaders.Stats() }),

		newFeature("edge_cache_rules", "/edge-cache-rules", func(id string, rc config.RouteConfig) error {
			merged := edgecacherules.MergeEdgeCacheRulesConfig(rc.EdgeCacheRules, cfg.EdgeCacheRules)
			if merged.Enabled {
				return rm.edgeCacheRules.AddRoute(id, merged)
			}
			return nil
		}, rm.edgeCacheRules.RouteIDs, func() any { return rm.edgeCacheRules.Stats() }),

		// ---- Global-scope features (setup is a no-op) ----

		newFeature("tenant", "/tenants", func(id string, rc config.RouteConfig) error {
			return nil
		}, func() []string {
			if rm.tenantManager != nil {
				return []string{"global"}
			}
			return nil
		}, func() any {
			if rm.tenantManager != nil {
				return rm.tenantManager.Stats()
			}
			return nil
		}),

		// ---- NoOp features: setup handled elsewhere (need transport/balancer) ----

		noOpStatsFeature("grpc_reflection", "/grpc-reflection", rm.grpcReflection),
		noOpStatsFeature("graphql_federation", "/graphql-federation", rm.federationHandlers),
		noOpStatsFeature("canary", "/canary", rm.canaryControllers),
		noOpStatsFeature("outlier_detection", "/outlier-detection", rm.outlierDetectors),
		noOpStatsFeature("backpressure", "/backpressure", rm.backpressureHandlers),
		noOpStatsFeature("blue_green", "/blue-green", rm.blueGreenControllers),
		noOpStatsFeature("ab_test", "/ab-tests", rm.abTests),
		noOpStatsFeature("sequential", "/sequential", rm.sequentialHandlers),
		noOpStatsFeature("aggregate", "/aggregate", rm.aggregateHandlers),
		noOpStatsFeature("lambda", "/lambda", rm.lambdaHandlers),
		noOpStatsFeature("amqp", "/amqp", rm.amqpHandlers),
		noOpStatsFeature("pubsub", "/pubsub", rm.pubsubHandlers),
		noOpStatsFeature("protocol_translators", "/protocol-translators", rm.translators),
		noOpStatsFeature("grpc_proxy", "/grpc-proxy", rm.grpcHandlers),

		noOpFeature("retry_budget_pools", "/retry-budget-pools", func() []string { return nil }, func() any {
			if len(rm.budgetPools) == 0 {
				return nil
			}
			result := make(map[string]interface{}, len(rm.budgetPools))
			for name, b := range rm.budgetPools {
				result[name] = b.Stats()
			}
			return result
		}),

		// Global singleton stats (no per-route setup, but admin stats)
		noOpFeature("trusted_proxies", "/trusted-proxies", func() []string { return nil }, func() any {
			if rm.realIPExtractor == nil {
				return map[string]interface{}{"enabled": false}
			}
			return rm.realIPExtractor.Stats()
		}),
	}
}

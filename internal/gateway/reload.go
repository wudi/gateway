package gateway

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/wudi/gateway/internal/cache"
	"github.com/wudi/gateway/internal/canary"
	"github.com/wudi/gateway/internal/circuitbreaker"
	"github.com/wudi/gateway/internal/coalesce"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/graphql"
	"github.com/wudi/gateway/internal/loadbalancer"
	"github.com/wudi/gateway/internal/loadbalancer/outlier"
	"github.com/wudi/gateway/internal/logging"
	"github.com/wudi/gateway/internal/middleware/accesslog"
	"github.com/wudi/gateway/internal/middleware/allowedhosts"
	"github.com/wudi/gateway/internal/middleware/auth"
	"github.com/wudi/gateway/internal/middleware/auditlog"
	"github.com/wudi/gateway/internal/middleware/backendauth"
	"github.com/wudi/gateway/internal/middleware/backpressure"
	"github.com/wudi/gateway/internal/middleware/baggage"
	"github.com/wudi/gateway/internal/middleware/backendenc"
	"github.com/wudi/gateway/internal/middleware/bodygen"
	"github.com/wudi/gateway/internal/middleware/compression"
	"github.com/wudi/gateway/internal/middleware/contentneg"
	"github.com/wudi/gateway/internal/middleware/contentreplacer"
	"github.com/wudi/gateway/internal/middleware/cors"
	"github.com/wudi/gateway/internal/middleware/debug"
	"github.com/wudi/gateway/internal/middleware/csrf"
	"github.com/wudi/gateway/internal/middleware/errorpages"
	"github.com/wudi/gateway/internal/middleware/extauth"
	"github.com/wudi/gateway/internal/middleware/httpsredirect"
	"github.com/wudi/gateway/internal/middleware/idempotency"
	"github.com/wudi/gateway/internal/middleware/ipfilter"
	"github.com/wudi/gateway/internal/middleware/loadshed"
	"github.com/wudi/gateway/internal/middleware/nonce"
	"github.com/wudi/gateway/internal/middleware/decompress"
	"github.com/wudi/gateway/internal/middleware/botdetect"
	"github.com/wudi/gateway/internal/middleware/cdnheaders"
	"github.com/wudi/gateway/internal/middleware/claimsprop"
	"github.com/wudi/gateway/internal/middleware/maintenance"
	"github.com/wudi/gateway/internal/middleware/mock"
	"github.com/wudi/gateway/internal/middleware/paramforward"
	"github.com/wudi/gateway/internal/middleware/requestqueue"
	"github.com/wudi/gateway/internal/middleware/proxyratelimit"
	"github.com/wudi/gateway/internal/middleware/quota"
	"github.com/wudi/gateway/internal/middleware/realip"
	"github.com/wudi/gateway/internal/middleware/tenant"
	"github.com/wudi/gateway/internal/middleware/respbodygen"
	"github.com/wudi/gateway/internal/middleware/securityheaders"
	"github.com/wudi/gateway/internal/middleware/serviceratelimit"
	"github.com/wudi/gateway/internal/middleware/sse"
	"github.com/wudi/gateway/internal/middleware/signing"
	"github.com/wudi/gateway/internal/middleware/spikearrest"
	"github.com/wudi/gateway/internal/middleware/staticfiles"
	"github.com/wudi/gateway/internal/middleware/statusmap"
	openapivalidation "github.com/wudi/gateway/internal/middleware/openapi"
	"github.com/wudi/gateway/internal/middleware/ratelimit"
	"github.com/wudi/gateway/internal/middleware/responselimit"
	"github.com/wudi/gateway/internal/middleware/timeout"
	"github.com/wudi/gateway/internal/middleware/tokenrevoke"
	"github.com/wudi/gateway/internal/middleware/validation"
	"github.com/wudi/gateway/internal/middleware/versioning"
	"github.com/wudi/gateway/internal/middleware/waf"
	"github.com/wudi/gateway/internal/mirror"
	"github.com/wudi/gateway/internal/proxy"
	"github.com/wudi/gateway/internal/retry"
	grpcproxy "github.com/wudi/gateway/internal/proxy/grpc"
	"github.com/wudi/gateway/internal/proxy/protocol"
	"github.com/wudi/gateway/internal/abtest"
	"github.com/wudi/gateway/internal/bluegreen"
	"github.com/wudi/gateway/internal/middleware/fieldencrypt"
	"github.com/wudi/gateway/internal/middleware/inboundsigning"
	"github.com/wudi/gateway/internal/middleware/piiredact"
	"github.com/wudi/gateway/internal/proxy/aggregate"
	"github.com/wudi/gateway/internal/proxy/sequential"
	"github.com/wudi/gateway/internal/registry"
	"github.com/wudi/gateway/internal/router"
	"github.com/wudi/gateway/internal/rules"
	"github.com/wudi/gateway/internal/trafficshape"
	"github.com/wudi/gateway/internal/variables"
	"github.com/wudi/gateway/internal/webhook"
	"github.com/wudi/gateway/internal/websocket"
	"go.uber.org/zap"
)

// ReloadResult represents the outcome of a config reload.
type ReloadResult struct {
	Success   bool      `json:"success"`
	Timestamp time.Time `json:"timestamp"`
	Error     string    `json:"error,omitempty"`
	Changes   []string  `json:"changes,omitempty"`
}

// gatewayState holds all route-scoped state that gets replaced during a reload.
type gatewayState struct {
	config        *config.Config
	router        *router.Router
	routeProxies  map[string]*proxy.RouteProxy
	routeHandlers map[string]http.Handler
	watchCancels  map[string]context.CancelFunc
	features      []Feature

	// ByRoute managers
	circuitBreakers   *circuitbreaker.BreakerByRoute
	caches            *cache.CacheByRoute
	ipFilters         *ipfilter.IPFilterByRoute
	globalIPFilter    *ipfilter.Filter
	corsHandlers      *cors.CORSByRoute
	compressors       *compression.CompressorByRoute
	validators        *validation.ValidatorByRoute
	mirrors           *mirror.MirrorByRoute
	routeRules        *rules.RulesByRoute
	globalRules       *rules.RuleEngine
	throttlers        *trafficshape.ThrottleByRoute
	bandwidthLimiters *trafficshape.BandwidthByRoute
	priorityAdmitter  *trafficshape.PriorityAdmitter
	priorityConfigs   *trafficshape.PriorityByRoute
	faultInjectors    *trafficshape.FaultInjectionByRoute
	wafHandlers       *waf.WAFByRoute
	graphqlParsers    *graphql.GraphQLByRoute
	coalescers        *coalesce.CoalesceByRoute
	canaryControllers *canary.CanaryByRoute
	adaptiveLimiters  *trafficshape.AdaptiveConcurrencyByRoute
	extAuths          *extauth.ExtAuthByRoute
	versioners        *versioning.VersioningByRoute
	accessLogConfigs  *accesslog.AccessLogByRoute
	openapiValidators *openapivalidation.OpenAPIByRoute
	timeoutConfigs    *timeout.TimeoutByRoute
	errorPages        *errorpages.ErrorPagesByRoute
	nonceCheckers     *nonce.NonceByRoute
	csrfProtectors      *csrf.CSRFByRoute
	idempotencyHandlers *idempotency.IdempotencyByRoute
	backendSigners      *signing.SigningByRoute
	decompressors       *decompress.DecompressorByRoute
	responseLimiters    *responselimit.ResponseLimitByRoute
	securityHeaders     *securityheaders.SecurityHeadersByRoute
	maintenanceHandlers *maintenance.MaintenanceByRoute
	botDetectors        *botdetect.BotDetectByRoute
	proxyRateLimiters   *proxyratelimit.ProxyRateLimitByRoute
	mockHandlers        *mock.MockByRoute
	claimsPropagators   *claimsprop.ClaimsPropByRoute
	backendAuths        *backendauth.BackendAuthByRoute
	statusMappers       *statusmap.StatusMapByRoute
	staticFiles         *staticfiles.StaticByRoute
	spikeArresters      *spikearrest.SpikeArrestByRoute
	contentReplacers    *contentreplacer.ContentReplacerByRoute
	bodyGenerators      *bodygen.BodyGenByRoute
	quotaEnforcers      *quota.QuotaByRoute
	sequentialHandlers  *sequential.SequentialByRoute
	aggregateHandlers   *aggregate.AggregateByRoute
	respBodyGenerators  *respbodygen.RespBodyGenByRoute
	paramForwarders     *paramforward.ParamForwardByRoute
	contentNegotiators  *contentneg.NegotiatorByRoute
	cdnHeaders          *cdnheaders.CDNHeadersByRoute
	backendEncoders     *backendenc.EncoderByRoute
	sseHandlers         *sse.SSEByRoute
	realIPExtractor     *realip.CompiledRealIP
	tokenChecker        *tokenrevoke.TokenChecker
	outlierDetectors    *outlier.DetectorByRoute
	inboundVerifiers     *inboundsigning.InboundSigningByRoute
	piiRedactors         *piiredact.PIIRedactByRoute
	fieldEncryptors      *fieldencrypt.FieldEncryptByRoute
	blueGreenControllers *bluegreen.BlueGreenByRoute
	abTests              *abtest.ABTestByRoute
	requestQueues        *requestqueue.RequestQueueByRoute
	translators       *protocol.TranslatorByRoute
	rateLimiters         *ratelimit.RateLimitByRoute
	grpcHandlers         *grpcproxy.GRPCByRoute
	budgetPools          map[string]*retry.Budget
	baggagePropagators   *baggage.BaggageByRoute
	backpressureHandlers *backpressure.BackpressureByRoute
	auditLoggers         *auditlog.AuditLogByRoute

	tenantManager *tenant.Manager

	// Auth providers
	apiKeyAuth *auth.APIKeyAuth
	jwtAuth    *auth.JWTAuth
	oauthAuth  *auth.OAuthAuth
}

// buildState builds all route-scoped state from a config.
// Shared infrastructure (proxy, healthChecker, registry, metricsCollector, redisClient, tracer) is
// passed via the Gateway and reused without replacement.
func (g *Gateway) buildState(cfg *config.Config) (*gatewayState, error) {
	s := &gatewayState{
		config:            cfg,
		router:            router.New(),
		routeProxies:      make(map[string]*proxy.RouteProxy),
		routeHandlers:     make(map[string]http.Handler),
		watchCancels:      make(map[string]context.CancelFunc),
		circuitBreakers:   circuitbreaker.NewBreakerByRoute(),
		caches:            cache.NewCacheByRoute(g.redisClient),
		ipFilters:         ipfilter.NewIPFilterByRoute(),
		corsHandlers:      cors.NewCORSByRoute(),
		compressors:       compression.NewCompressorByRoute(),
		validators:        validation.NewValidatorByRoute(),
		mirrors:           mirror.NewMirrorByRoute(),
		routeRules:        rules.NewRulesByRoute(),
		throttlers:        trafficshape.NewThrottleByRoute(),
		bandwidthLimiters: trafficshape.NewBandwidthByRoute(),
		priorityConfigs:   trafficshape.NewPriorityByRoute(),
		faultInjectors:    trafficshape.NewFaultInjectionByRoute(),
		wafHandlers:       waf.NewWAFByRoute(),
		graphqlParsers:    graphql.NewGraphQLByRoute(),
		coalescers:        coalesce.NewCoalesceByRoute(),
		canaryControllers: canary.NewCanaryByRoute(),
		adaptiveLimiters:  trafficshape.NewAdaptiveConcurrencyByRoute(),
		extAuths:          extauth.NewExtAuthByRoute(),
		versioners:        versioning.NewVersioningByRoute(),
		accessLogConfigs:  accesslog.NewAccessLogByRoute(),
		openapiValidators: openapivalidation.NewOpenAPIByRoute(),
		timeoutConfigs:    timeout.NewTimeoutByRoute(),
		errorPages:        errorpages.NewErrorPagesByRoute(),
		nonceCheckers:     nonce.NewNonceByRoute(),
		csrfProtectors:      csrf.NewCSRFByRoute(),
		idempotencyHandlers: idempotency.NewIdempotencyByRoute(),
		backendSigners:      signing.NewSigningByRoute(),
		decompressors:       decompress.NewDecompressorByRoute(),
		responseLimiters:    responselimit.NewResponseLimitByRoute(),
		securityHeaders:     securityheaders.NewSecurityHeadersByRoute(),
		maintenanceHandlers: maintenance.NewMaintenanceByRoute(),
		botDetectors:        botdetect.NewBotDetectByRoute(),
		proxyRateLimiters:   proxyratelimit.NewProxyRateLimitByRoute(),
		mockHandlers:        mock.NewMockByRoute(),
		claimsPropagators:   claimsprop.NewClaimsPropByRoute(),
		backendAuths:        backendauth.NewBackendAuthByRoute(),
		statusMappers:       statusmap.NewStatusMapByRoute(),
		staticFiles:         staticfiles.NewStaticByRoute(),
		spikeArresters:      spikearrest.NewSpikeArrestByRoute(),
		contentReplacers:    contentreplacer.NewContentReplacerByRoute(),
		bodyGenerators:      bodygen.NewBodyGenByRoute(),
		quotaEnforcers:      quota.NewQuotaByRoute(),
		sequentialHandlers:  sequential.NewSequentialByRoute(),
		aggregateHandlers:   aggregate.NewAggregateByRoute(),
		respBodyGenerators:  respbodygen.NewRespBodyGenByRoute(),
		paramForwarders:     paramforward.NewParamForwardByRoute(),
		contentNegotiators:  contentneg.NewNegotiatorByRoute(),
		cdnHeaders:          cdnheaders.NewCDNHeadersByRoute(),
		backendEncoders:     backendenc.NewEncoderByRoute(),
		sseHandlers:         sse.NewSSEByRoute(),
		outlierDetectors:    outlier.NewDetectorByRoute(),
		inboundVerifiers:     inboundsigning.NewInboundSigningByRoute(),
		piiRedactors:         piiredact.NewPIIRedactByRoute(),
		fieldEncryptors:      fieldencrypt.NewFieldEncryptByRoute(),
		blueGreenControllers: bluegreen.NewBlueGreenByRoute(),
		abTests:              abtest.NewABTestByRoute(),
		requestQueues:        requestqueue.NewRequestQueueByRoute(),
		translators:       protocol.NewTranslatorByRoute(),
		rateLimiters:      ratelimit.NewRateLimitByRoute(),
		grpcHandlers:         grpcproxy.NewGRPCByRoute(),
		budgetPools:          make(map[string]*retry.Budget),
		baggagePropagators:   baggage.NewBaggageByRoute(),
		backpressureHandlers: backpressure.NewBackpressureByRoute(),
		auditLoggers:         auditlog.NewAuditLogByRoute(),
	}

	// Initialize tenant manager
	if cfg.Tenants.Enabled {
		s.tenantManager = tenant.NewManager(cfg.Tenants, g.redisClient)
	}

	// Initialize shared retry budget pools
	for name, bc := range cfg.RetryBudgets {
		s.budgetPools[name] = retry.NewBudget(bc.Ratio, bc.MinRetries, bc.Window)
	}

	// Initialize shared priority admitter if global priority is enabled
	if cfg.TrafficShaping.Priority.Enabled {
		s.priorityAdmitter = trafficshape.NewPriorityAdmitter(cfg.TrafficShaping.Priority.MaxConcurrent)
	}

	// Register features
	s.features = []Feature{
		newFeature("ip_filter", "", func(id string, rc config.RouteConfig) error {
			if rc.IPFilter.Enabled { return s.ipFilters.AddRoute(id, rc.IPFilter) }
			return nil
		}, s.ipFilters.RouteIDs, nil),
		newFeature("cors", "", func(id string, rc config.RouteConfig) error {
			if rc.CORS.Enabled { return s.corsHandlers.AddRoute(id, rc.CORS) }
			return nil
		}, s.corsHandlers.RouteIDs, nil),
		newFeature("circuit_breaker", "/circuit-breakers", func(id string, rc config.RouteConfig) error {
			if rc.CircuitBreaker.Enabled {
				if rc.CircuitBreaker.Mode == "distributed" && g.redisClient != nil {
					s.circuitBreakers.AddRouteDistributed(id, rc.CircuitBreaker, g.redisClient)
				} else {
					s.circuitBreakers.AddRoute(id, rc.CircuitBreaker)
				}
			}
			return nil
		}, s.circuitBreakers.RouteIDs, func() any { return s.circuitBreakers.Snapshots() }),
		newFeature("cache", "/cache", func(id string, rc config.RouteConfig) error {
			if rc.Cache.Enabled { s.caches.AddRoute(id, rc.Cache) }
			return nil
		}, s.caches.RouteIDs, func() any { return s.caches.Stats() }),
		newFeature("compression", "/compression", func(id string, rc config.RouteConfig) error {
			if rc.Compression.Enabled { s.compressors.AddRoute(id, rc.Compression) }
			return nil
		}, s.compressors.RouteIDs, func() any { return s.compressors.Stats() }),
		newFeature("validation", "", func(id string, rc config.RouteConfig) error {
			if rc.Validation.Enabled { return s.validators.AddRoute(id, rc.Validation) }
			return nil
		}, s.validators.RouteIDs, nil),
		newFeature("mirror", "/mirrors", func(id string, rc config.RouteConfig) error {
			if rc.Mirror.Enabled { return s.mirrors.AddRoute(id, rc.Mirror) }
			return nil
		}, s.mirrors.RouteIDs, func() any { return s.mirrors.Stats() }),
		newFeature("rules", "", func(id string, rc config.RouteConfig) error {
			if len(rc.Rules.Request) > 0 || len(rc.Rules.Response) > 0 { return s.routeRules.AddRoute(id, rc.Rules) }
			return nil
		}, s.routeRules.RouteIDs, func() any { return s.routeRules.Stats() }),
		newFeature("throttle", "", func(id string, rc config.RouteConfig) error {
			tc := rc.TrafficShaping.Throttle
			if tc.Enabled {
				s.throttlers.AddRoute(id, trafficshape.MergeThrottleConfig(tc, cfg.TrafficShaping.Throttle))
			} else if cfg.TrafficShaping.Throttle.Enabled {
				s.throttlers.AddRoute(id, cfg.TrafficShaping.Throttle)
			}
			return nil
		}, s.throttlers.RouteIDs, func() any { return s.throttlers.Stats() }),
		newFeature("bandwidth", "", func(id string, rc config.RouteConfig) error {
			bc := rc.TrafficShaping.Bandwidth
			if bc.Enabled {
				s.bandwidthLimiters.AddRoute(id, trafficshape.MergeBandwidthConfig(bc, cfg.TrafficShaping.Bandwidth))
			} else if cfg.TrafficShaping.Bandwidth.Enabled {
				s.bandwidthLimiters.AddRoute(id, cfg.TrafficShaping.Bandwidth)
			}
			return nil
		}, s.bandwidthLimiters.RouteIDs, func() any { return s.bandwidthLimiters.Stats() }),
		newFeature("priority", "", func(id string, rc config.RouteConfig) error {
			pc := rc.TrafficShaping.Priority
			if pc.Enabled {
				s.priorityConfigs.AddRoute(id, trafficshape.MergePriorityConfig(pc, cfg.TrafficShaping.Priority))
			} else if cfg.TrafficShaping.Priority.Enabled {
				s.priorityConfigs.AddRoute(id, cfg.TrafficShaping.Priority)
			}
			return nil
		}, s.priorityConfigs.RouteIDs, nil),
		newFeature("fault_injection", "", func(id string, rc config.RouteConfig) error {
			fi := rc.TrafficShaping.FaultInjection
			if fi.Enabled {
				s.faultInjectors.AddRoute(id, trafficshape.MergeFaultInjectionConfig(fi, cfg.TrafficShaping.FaultInjection))
			} else if cfg.TrafficShaping.FaultInjection.Enabled {
				s.faultInjectors.AddRoute(id, cfg.TrafficShaping.FaultInjection)
			}
			return nil
		}, s.faultInjectors.RouteIDs, func() any { return s.faultInjectors.Stats() }),
		newFeature("adaptive_concurrency", "/adaptive-concurrency", func(id string, rc config.RouteConfig) error {
			ac := rc.TrafficShaping.AdaptiveConcurrency
			if ac.Enabled {
				s.adaptiveLimiters.AddRoute(id, trafficshape.MergeAdaptiveConcurrencyConfig(ac, cfg.TrafficShaping.AdaptiveConcurrency))
			} else if cfg.TrafficShaping.AdaptiveConcurrency.Enabled {
				s.adaptiveLimiters.AddRoute(id, cfg.TrafficShaping.AdaptiveConcurrency)
			}
			return nil
		}, s.adaptiveLimiters.RouteIDs, func() any { return s.adaptiveLimiters.Stats() }),
		newFeature("request_queue", "/request-queues", func(id string, rc config.RouteConfig) error {
			rq := rc.TrafficShaping.RequestQueue
			if rq.Enabled {
				s.requestQueues.AddRoute(id, requestqueue.MergeRequestQueueConfig(rq, cfg.TrafficShaping.RequestQueue))
			} else if cfg.TrafficShaping.RequestQueue.Enabled {
				s.requestQueues.AddRoute(id, cfg.TrafficShaping.RequestQueue)
			}
			return nil
		}, s.requestQueues.RouteIDs, func() any { return s.requestQueues.Stats() }),
		newFeature("waf", "/waf", func(id string, rc config.RouteConfig) error {
			if rc.WAF.Enabled { return s.wafHandlers.AddRoute(id, rc.WAF) }
			return nil
		}, s.wafHandlers.RouteIDs, func() any { return s.wafHandlers.Stats() }),
		newFeature("graphql", "/graphql", func(id string, rc config.RouteConfig) error {
			if rc.GraphQL.Enabled { return s.graphqlParsers.AddRoute(id, rc.GraphQL) }
			return nil
		}, s.graphqlParsers.RouteIDs, func() any { return s.graphqlParsers.Stats() }),
		newFeature("coalesce", "/coalesce", func(id string, rc config.RouteConfig) error {
			if rc.Coalesce.Enabled { s.coalescers.AddRoute(id, rc.Coalesce) }
			return nil
		}, s.coalescers.RouteIDs, func() any { return s.coalescers.Stats() }),
		noOpFeature("canary", "/canary", s.canaryControllers.RouteIDs, func() any { return s.canaryControllers.Stats() }),
		newFeature("ext_auth", "/ext-auth", func(id string, rc config.RouteConfig) error {
			if rc.ExtAuth.Enabled { return s.extAuths.AddRoute(id, rc.ExtAuth) }
			return nil
		}, s.extAuths.RouteIDs, func() any { return s.extAuths.Stats() }),
		newFeature("versioning", "/versioning", func(id string, rc config.RouteConfig) error {
			if rc.Versioning.Enabled { return s.versioners.AddRoute(id, rc.Versioning) }
			return nil
		}, s.versioners.RouteIDs, func() any { return s.versioners.Stats() }),
		newFeature("access_log", "/access-log", func(id string, rc config.RouteConfig) error {
			al := rc.AccessLog
			if al.Enabled != nil || al.Format != "" || len(al.HeadersInclude) > 0 || len(al.HeadersExclude) > 0 ||
				al.Body.Enabled || al.Conditions.SampleRate > 0 || len(al.Conditions.StatusCodes) > 0 || len(al.Conditions.Methods) > 0 {
				return s.accessLogConfigs.AddRoute(id, al)
			}
			return nil
		}, s.accessLogConfigs.RouteIDs, func() any { return s.accessLogConfigs.Stats() }),
		newFeature("openapi", "/openapi", func(id string, rc config.RouteConfig) error {
			if rc.OpenAPI.SpecFile != "" || rc.OpenAPI.SpecID != "" { return s.openapiValidators.AddRoute(id, rc.OpenAPI) }
			return nil
		}, s.openapiValidators.RouteIDs, func() any { return s.openapiValidators.Stats() }),
		newFeature("timeout", "/timeouts", func(id string, rc config.RouteConfig) error {
			if rc.TimeoutPolicy.IsActive() { s.timeoutConfigs.AddRoute(id, rc.TimeoutPolicy) }
			return nil
		}, s.timeoutConfigs.RouteIDs, func() any { return s.timeoutConfigs.Stats() }),
		newFeature("error_pages", "/error-pages", func(id string, rc config.RouteConfig) error {
			if cfg.ErrorPages.IsActive() || rc.ErrorPages.IsActive() { return s.errorPages.AddRoute(id, cfg.ErrorPages, rc.ErrorPages) }
			return nil
		}, s.errorPages.RouteIDs, func() any { return s.errorPages.Stats() }),
		newFeature("nonce", "/nonces", func(id string, rc config.RouteConfig) error {
			if rc.Nonce.Enabled { return s.nonceCheckers.AddRoute(id, nonce.MergeNonceConfig(rc.Nonce, cfg.Nonce), g.redisClient) }
			if cfg.Nonce.Enabled { return s.nonceCheckers.AddRoute(id, cfg.Nonce, g.redisClient) }
			return nil
		}, s.nonceCheckers.RouteIDs, func() any { return s.nonceCheckers.Stats() }),
		newFeature("csrf", "/csrf", func(id string, rc config.RouteConfig) error {
			if rc.CSRF.Enabled { return s.csrfProtectors.AddRoute(id, csrf.MergeCSRFConfig(rc.CSRF, cfg.CSRF)) }
			if cfg.CSRF.Enabled { return s.csrfProtectors.AddRoute(id, cfg.CSRF) }
			return nil
		}, s.csrfProtectors.RouteIDs, func() any { return s.csrfProtectors.Stats() }),
		newFeature("idempotency", "/idempotency", func(id string, rc config.RouteConfig) error {
			if rc.Idempotency.Enabled { return s.idempotencyHandlers.AddRoute(id, idempotency.MergeIdempotencyConfig(rc.Idempotency, cfg.Idempotency), g.redisClient) }
			if cfg.Idempotency.Enabled { return s.idempotencyHandlers.AddRoute(id, cfg.Idempotency, g.redisClient) }
			return nil
		}, s.idempotencyHandlers.RouteIDs, func() any { return s.idempotencyHandlers.Stats() }),
		noOpFeature("outlier_detection", "/outlier-detection", s.outlierDetectors.RouteIDs, func() any { return s.outlierDetectors.Stats() }),
		newFeature("backend_signing", "/signing", func(id string, rc config.RouteConfig) error {
			if rc.BackendSigning.Enabled { return s.backendSigners.AddRoute(id, signing.MergeSigningConfig(rc.BackendSigning, cfg.BackendSigning)) }
			if cfg.BackendSigning.Enabled { return s.backendSigners.AddRoute(id, cfg.BackendSigning) }
			return nil
		}, s.backendSigners.RouteIDs, func() any { return s.backendSigners.Stats() }),
		newFeature("request_decompression", "/decompression", func(id string, rc config.RouteConfig) error {
			if rc.RequestDecompression.Enabled {
				s.decompressors.AddRoute(id, decompress.MergeDecompressionConfig(rc.RequestDecompression, cfg.RequestDecompression))
			} else if cfg.RequestDecompression.Enabled {
				s.decompressors.AddRoute(id, cfg.RequestDecompression)
			}
			return nil
		}, s.decompressors.RouteIDs, func() any { return s.decompressors.Stats() }),
		newFeature("response_limit", "/response-limits", func(id string, rc config.RouteConfig) error {
			if rc.ResponseLimit.Enabled {
				s.responseLimiters.AddRoute(id, responselimit.MergeResponseLimitConfig(rc.ResponseLimit, cfg.ResponseLimit))
			} else if cfg.ResponseLimit.Enabled {
				s.responseLimiters.AddRoute(id, cfg.ResponseLimit)
			}
			return nil
		}, s.responseLimiters.RouteIDs, func() any { return s.responseLimiters.Stats() }),
		newFeature("security_headers", "/security-headers", func(id string, rc config.RouteConfig) error {
			if rc.SecurityHeaders.Enabled {
				s.securityHeaders.AddRoute(id, securityheaders.MergeSecurityHeadersConfig(rc.SecurityHeaders, cfg.SecurityHeaders))
			} else if cfg.SecurityHeaders.Enabled {
				s.securityHeaders.AddRoute(id, cfg.SecurityHeaders)
			}
			return nil
		}, s.securityHeaders.RouteIDs, func() any { return s.securityHeaders.Stats() }),
		newFeature("maintenance", "/maintenance", func(id string, rc config.RouteConfig) error {
			if rc.Maintenance.Enabled {
				s.maintenanceHandlers.AddRoute(id, maintenance.MergeMaintenanceConfig(rc.Maintenance, cfg.Maintenance))
			} else if cfg.Maintenance.Enabled {
				s.maintenanceHandlers.AddRoute(id, cfg.Maintenance)
			}
			return nil
		}, s.maintenanceHandlers.RouteIDs, func() any { return s.maintenanceHandlers.Stats() }),
		newFeature("bot_detection", "/bot-detection", func(id string, rc config.RouteConfig) error {
			if rc.BotDetection.Enabled { return s.botDetectors.AddRoute(id, botdetect.MergeBotDetectionConfig(rc.BotDetection, cfg.BotDetection)) }
			if cfg.BotDetection.Enabled { return s.botDetectors.AddRoute(id, cfg.BotDetection) }
			return nil
		}, s.botDetectors.RouteIDs, func() any { return s.botDetectors.Stats() }),
		newFeature("proxy_rate_limit", "/proxy-rate-limits", func(id string, rc config.RouteConfig) error {
			if rc.ProxyRateLimit.Enabled { s.proxyRateLimiters.AddRoute(id, rc.ProxyRateLimit) }
			return nil
		}, s.proxyRateLimiters.RouteIDs, func() any { return s.proxyRateLimiters.Stats() }),
		newFeature("mock_response", "/mock-responses", func(id string, rc config.RouteConfig) error {
			if rc.MockResponse.Enabled { s.mockHandlers.AddRoute(id, rc.MockResponse) }
			return nil
		}, s.mockHandlers.RouteIDs, func() any { return s.mockHandlers.Stats() }),
		newFeature("claims_propagation", "/claims-propagation", func(id string, rc config.RouteConfig) error {
			if rc.ClaimsPropagation.Enabled { s.claimsPropagators.AddRoute(id, rc.ClaimsPropagation) }
			return nil
		}, s.claimsPropagators.RouteIDs, func() any { return s.claimsPropagators.Stats() }),
		newFeature("backend_auth", "/backend-auth", func(id string, rc config.RouteConfig) error {
			if rc.BackendAuth.Enabled { return s.backendAuths.AddRoute(id, rc.BackendAuth) }
			return nil
		}, s.backendAuths.RouteIDs, func() any { return s.backendAuths.Stats() }),
		newFeature("status_mapping", "/status-mapping", func(id string, rc config.RouteConfig) error {
			if rc.StatusMapping.Enabled && len(rc.StatusMapping.Mappings) > 0 { s.statusMappers.AddRoute(id, rc.StatusMapping.Mappings) }
			return nil
		}, s.statusMappers.RouteIDs, func() any { return s.statusMappers.Stats() }),
		newFeature("static_files", "/static-files", func(id string, rc config.RouteConfig) error {
			if rc.Static.Enabled { return s.staticFiles.AddRoute(id, rc.Static.Root, rc.Static.Index, rc.Static.Browse, rc.Static.CacheControl) }
			return nil
		}, s.staticFiles.RouteIDs, func() any { return s.staticFiles.Stats() }),
		newFeature("spike_arrest", "/spike-arrest", func(id string, rc config.RouteConfig) error {
			if !rc.SpikeArrest.Enabled && !cfg.SpikeArrest.Enabled { return nil }
			s.spikeArresters.AddRoute(id, spikearrest.MergeSpikeArrestConfig(rc.SpikeArrest, cfg.SpikeArrest))
			return nil
		}, s.spikeArresters.RouteIDs, func() any { return s.spikeArresters.Stats() }),
		newFeature("content_replacer", "/content-replacer", func(id string, rc config.RouteConfig) error {
			if rc.ContentReplacer.Enabled && len(rc.ContentReplacer.Replacements) > 0 { return s.contentReplacers.AddRoute(id, rc.ContentReplacer) }
			return nil
		}, s.contentReplacers.RouteIDs, func() any { return s.contentReplacers.Stats() }),
		newFeature("body_generator", "/body-generator", func(id string, rc config.RouteConfig) error {
			if rc.BodyGenerator.Enabled { return s.bodyGenerators.AddRoute(id, rc.BodyGenerator) }
			return nil
		}, s.bodyGenerators.RouteIDs, func() any { return s.bodyGenerators.Stats() }),
		newFeature("quota", "/quotas", func(id string, rc config.RouteConfig) error {
			if rc.Quota.Enabled { s.quotaEnforcers.AddRoute(id, rc.Quota, g.redisClient) }
			return nil
		}, s.quotaEnforcers.RouteIDs, func() any { return s.quotaEnforcers.Stats() }),
		newFeature("tenant", "/tenants", func(id string, rc config.RouteConfig) error {
			return nil
		}, func() []string {
			if s.tenantManager != nil { return []string{"global"} }; return nil
		}, func() any {
			if s.tenantManager != nil { return s.tenantManager.Stats() }; return nil
		}),
		noOpFeature("sequential", "/sequential", s.sequentialHandlers.RouteIDs, func() any { return s.sequentialHandlers.Stats() }),
		noOpFeature("aggregate", "/aggregate", s.aggregateHandlers.RouteIDs, func() any { return s.aggregateHandlers.Stats() }),
		newFeature("response_body_generator", "/response-body-generator", func(id string, rc config.RouteConfig) error {
			if rc.ResponseBodyGenerator.Enabled { return s.respBodyGenerators.AddRoute(id, rc.ResponseBodyGenerator) }
			return nil
		}, s.respBodyGenerators.RouteIDs, func() any { return s.respBodyGenerators.Stats() }),
		newFeature("param_forwarding", "/param-forwarding", func(id string, rc config.RouteConfig) error {
			if rc.ParamForwarding.Enabled { s.paramForwarders.AddRoute(id, rc.ParamForwarding) }
			return nil
		}, s.paramForwarders.RouteIDs, func() any { return s.paramForwarders.Stats() }),
		newFeature("content_negotiation", "/content-negotiation", func(id string, rc config.RouteConfig) error {
			if rc.ContentNegotiation.Enabled { return s.contentNegotiators.AddRoute(id, rc.ContentNegotiation) }
			return nil
		}, s.contentNegotiators.RouteIDs, func() any { return s.contentNegotiators.Stats() }),
		newFeature("cdn_cache_headers", "/cdn-cache-headers", func(id string, rc config.RouteConfig) error {
			merged := cdnheaders.MergeCDNCacheConfig(rc.CDNCacheHeaders, cfg.CDNCacheHeaders)
			if merged.Enabled { s.cdnHeaders.AddRoute(id, merged) }
			return nil
		}, s.cdnHeaders.RouteIDs, func() any { return s.cdnHeaders.Stats() }),
		newFeature("backend_encoding", "/backend-encoding", func(id string, rc config.RouteConfig) error {
			if rc.BackendEncoding.Encoding != "" { s.backendEncoders.AddRoute(id, rc.BackendEncoding) }
			return nil
		}, s.backendEncoders.RouteIDs, func() any { return s.backendEncoders.Stats() }),
		newFeature("sse", "/sse", func(id string, rc config.RouteConfig) error {
			if rc.SSE.Enabled { s.sseHandlers.AddRoute(id, rc.SSE) }
			return nil
		}, s.sseHandlers.RouteIDs, func() any { return s.sseHandlers.Stats() }),

		newFeature("pii_redaction", "/pii-redaction", func(id string, rc config.RouteConfig) error {
			if rc.PIIRedaction.Enabled {
				return s.piiRedactors.AddRoute(id, rc.PIIRedaction)
			}
			return nil
		}, s.piiRedactors.RouteIDs, func() any { return s.piiRedactors.Stats() }),
		newFeature("field_encryption", "/field-encryption", func(id string, rc config.RouteConfig) error {
			if rc.FieldEncryption.Enabled {
				return s.fieldEncryptors.AddRoute(id, rc.FieldEncryption)
			}
			return nil
		}, s.fieldEncryptors.RouteIDs, func() any { return s.fieldEncryptors.Stats() }),
		newFeature("inbound_signing", "/inbound-signing", func(id string, rc config.RouteConfig) error {
			if rc.InboundSigning.Enabled {
				merged := inboundsigning.MergeInboundSigningConfig(rc.InboundSigning, cfg.InboundSigning)
				return s.inboundVerifiers.AddRoute(id, merged)
			}
			if cfg.InboundSigning.Enabled {
				return s.inboundVerifiers.AddRoute(id, cfg.InboundSigning)
			}
			return nil
		}, s.inboundVerifiers.RouteIDs, func() any { return s.inboundVerifiers.Stats() }),
		newFeature("baggage", "/baggage", func(id string, rc config.RouteConfig) error {
			if rc.Baggage.Enabled { return s.baggagePropagators.AddRoute(id, rc.Baggage) }
			return nil
		}, s.baggagePropagators.RouteIDs, func() any { return s.baggagePropagators.Stats() }),
		newFeature("audit_log", "/audit-log", func(id string, rc config.RouteConfig) error {
			if rc.AuditLog.Enabled {
				return s.auditLoggers.AddRoute(id, auditlog.MergeAuditLogConfig(rc.AuditLog, cfg.AuditLog))
			}
			if cfg.AuditLog.Enabled { return s.auditLoggers.AddRoute(id, cfg.AuditLog) }
			return nil
		}, s.auditLoggers.RouteIDs, func() any { return s.auditLoggers.Stats() }),
		noOpFeature("backpressure", "/backpressure", s.backpressureHandlers.RouteIDs, func() any { return s.backpressureHandlers.Stats() }),
		noOpFeature("blue_green", "/blue-green", s.blueGreenControllers.RouteIDs, func() any { return s.blueGreenControllers.Stats() }),
		noOpFeature("ab_test", "/ab-tests", s.abTests.RouteIDs, func() any { return s.abTests.Stats() }),

		// Non-per-route singleton features
		noOpFeature("retry_budget_pools", "/retry-budget-pools", func() []string { return nil }, func() any {
			if len(s.budgetPools) == 0 { return nil }
			result := make(map[string]interface{}, len(s.budgetPools))
			for name, b := range s.budgetPools { result[name] = b.Stats() }
			return result
		}),
	}

	// Initialize token revocation checker
	if cfg.TokenRevocation.Enabled {
		s.tokenChecker = tokenrevoke.New(cfg.TokenRevocation, g.redisClient)
	}

	// Wire webhook callbacks on new state's managers
	if g.webhookDispatcher != nil {
		s.circuitBreakers.SetOnStateChange(func(routeID, from, to string) {
			g.webhookDispatcher.Emit(webhook.NewEvent(webhook.CircuitBreakerStateChange, routeID, map[string]interface{}{
				"from": from, "to": to,
			}))
		})
		s.canaryControllers.SetOnEvent(func(routeID, eventType string, data map[string]interface{}) {
			g.webhookDispatcher.Emit(webhook.NewEvent(webhook.EventType(eventType), routeID, data))
		})
		s.outlierDetectors.SetCallbacks(
			func(routeID, backend, reason string) {
				g.webhookDispatcher.Emit(webhook.NewEvent(webhook.OutlierEjected, routeID, map[string]interface{}{
					"backend": backend, "reason": reason,
				}))
			},
			func(routeID, backend string) {
				g.webhookDispatcher.Emit(webhook.NewEvent(webhook.OutlierRecovered, routeID, map[string]interface{}{
					"backend": backend,
				}))
			},
		)
	}

	// Initialize global IP filter
	if cfg.IPFilter.Enabled {
		var err error
		s.globalIPFilter, err = ipfilter.New(cfg.IPFilter)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize global IP filter: %w", err)
		}
	}

	// Initialize trusted proxies / real IP extractor
	if len(cfg.TrustedProxies.CIDRs) > 0 {
		var err error
		s.realIPExtractor, err = realip.New(cfg.TrustedProxies.CIDRs, cfg.TrustedProxies.Headers, cfg.TrustedProxies.MaxHops)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize trusted proxies: %w", err)
		}
	}

	// Initialize global rules engine
	if len(cfg.Rules.Request) > 0 || len(cfg.Rules.Response) > 0 {
		var err error
		s.globalRules, err = rules.NewEngine(cfg.Rules.Request, cfg.Rules.Response)
		if err != nil {
			return nil, fmt.Errorf("failed to compile global rules: %w", err)
		}
	}

	// Initialize auth
	if cfg.Authentication.APIKey.Enabled {
		s.apiKeyAuth = auth.NewAPIKeyAuth(cfg.Authentication.APIKey)
	}
	if cfg.Authentication.JWT.Enabled {
		var err error
		s.jwtAuth, err = auth.NewJWTAuth(cfg.Authentication.JWT)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize JWT auth: %w", err)
		}
	}
	if cfg.Authentication.OAuth.Enabled {
		var err error
		s.oauthAuth, err = auth.NewOAuthAuth(cfg.Authentication.OAuth)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize OAuth auth: %w", err)
		}
	}

	// Rebuild transport pool from new config and swap onto shared proxy
	newPool := g.buildTransportPool(cfg)
	oldPool := g.proxy.GetTransportPool()
	g.proxy.SetTransportPool(newPool)
	if oldPool != nil {
		oldPool.CloseIdleConnections()
	}

	// Initialize each route using a temporary Gateway view so addRouteForState works
	for _, routeCfg := range cfg.Routes {
		if err := g.addRouteForState(s, routeCfg); err != nil {
			// Clean up translators on failure
			s.translators.Close()
			return nil, fmt.Errorf("failed to add route %s: %w", routeCfg.ID, err)
		}
	}

	return s, nil
}

// addRouteForState adds a single route into the given gatewayState, using the Gateway's
// shared infrastructure (proxy, healthChecker, registry, redisClient).
func (g *Gateway) addRouteForState(s *gatewayState, routeCfg config.RouteConfig) error {
	// Resolve upstream references into inline backends/service/LB settings
	routeCfg = resolveUpstreamRefs(s.config, routeCfg)

	if err := s.router.AddRoute(routeCfg); err != nil {
		return err
	}

	route := s.router.GetRoute(routeCfg.ID)
	if route == nil {
		return fmt.Errorf("route not found after adding: %s", routeCfg.ID)
	}

	// Set upstream name on route for transport pool resolution
	route.UpstreamName = routeCfg.Upstream

	var backends []*loadbalancer.Backend

	if routeCfg.Service.Name != "" {
		ctx := context.Background()
		services, err := g.registry.DiscoverWithTags(ctx, routeCfg.Service.Name, routeCfg.Service.Tags)
		if err != nil {
			logging.Warn("Failed to discover service during reload",
				zap.String("service", routeCfg.Service.Name),
				zap.Error(err),
			)
		}
		for _, svc := range services {
			b := &loadbalancer.Backend{
				URL:     svc.URL(),
				Weight:  1,
				Healthy: svc.Health == registry.HealthPassing,
			}
			b.InitParsedURL()
			backends = append(backends, b)
		}

		// Watch service in the context of the new state
		watchCtx, cancel := context.WithCancel(context.Background())
		s.watchCancels[routeCfg.ID] = cancel
		go g.watchServiceForState(s, watchCtx, routeCfg.ID, routeCfg.Service.Name, routeCfg.Service.Tags)
	} else {
		var usHC *config.HealthCheckConfig
		if routeCfg.Upstream != "" {
			if us, ok := s.config.Upstreams[routeCfg.Upstream]; ok {
				usHC = us.HealthCheck
			}
		}
		for _, b := range routeCfg.Backends {
			weight := b.Weight
			if weight == 0 {
				weight = 1
			}
			be := &loadbalancer.Backend{
				URL:     b.URL,
				Weight:  weight,
				Healthy: true,
			}
			be.InitParsedURL()
			backends = append(backends, be)

			// Register backend with health checker
			g.healthChecker.UpdateBackend(upstreamHealthCheck(b.URL, s.config.HealthCheck, usHC, b.HealthCheck))
		}
	}

	// Create route proxy
	if routeCfg.Versioning.Enabled {
		versionBackends := make(map[string][]*loadbalancer.Backend)
		for ver, vcfg := range routeCfg.Versioning.Versions {
			var vBacks []*loadbalancer.Backend
			var verUSHC *config.HealthCheckConfig
			if vcfg.Upstream != "" {
				if us, ok := s.config.Upstreams[vcfg.Upstream]; ok {
					verUSHC = us.HealthCheck
				}
			}
			for _, b := range vcfg.Backends {
				weight := b.Weight
				if weight == 0 {
					weight = 1
				}
				vbe := &loadbalancer.Backend{URL: b.URL, Weight: weight, Healthy: true}
				vbe.InitParsedURL()
				vBacks = append(vBacks, vbe)
				g.healthChecker.UpdateBackend(upstreamHealthCheck(b.URL, s.config.HealthCheck, verUSHC, b.HealthCheck))
			}
			versionBackends[ver] = vBacks
		}
		vb := loadbalancer.NewVersionedBalancer(versionBackends, routeCfg.Versioning.DefaultVersion)
		s.routeProxies[routeCfg.ID] = proxy.NewRouteProxyWithBalancer(g.proxy, route, vb)
	} else if len(routeCfg.TrafficSplit) > 0 {
		var wb *loadbalancer.WeightedBalancer
		if routeCfg.Sticky.Enabled {
			wb = loadbalancer.NewWeightedBalancerWithSticky(routeCfg.TrafficSplit, routeCfg.Sticky)
		} else {
			wb = loadbalancer.NewWeightedBalancer(routeCfg.TrafficSplit)
		}
		s.routeProxies[routeCfg.ID] = proxy.NewRouteProxyWithBalancer(g.proxy, route, wb)
	} else {
		balancer := createBalancer(routeCfg, backends)
		s.routeProxies[routeCfg.ID] = proxy.NewRouteProxyWithBalancer(g.proxy, route, balancer)
	}

	// Wire shared retry budget pool if configured
	if routeCfg.RetryPolicy.BudgetPool != "" {
		if pool, ok := s.budgetPools[routeCfg.RetryPolicy.BudgetPool]; ok {
			if rp := s.routeProxies[routeCfg.ID]; rp != nil {
				rp.SetRetryBudget(pool)
			}
		}
	}

	// Rate limiting
	if len(routeCfg.RateLimit.Tiers) > 0 {
		tiers := make(map[string]ratelimit.Config, len(routeCfg.RateLimit.Tiers))
		for name, tc := range routeCfg.RateLimit.Tiers {
			tiers[name] = ratelimit.Config{
				Rate:   tc.Rate,
				Period: tc.Period,
				Burst:  tc.Burst,
			}
		}
		var keyFn func(*http.Request) string
		if routeCfg.RateLimit.Key != "" {
			keyFn = ratelimit.BuildKeyFunc(false, routeCfg.RateLimit.Key)
		}
		s.rateLimiters.AddRouteTiered(routeCfg.ID, ratelimit.TieredConfig{
			Tiers:       tiers,
			TierKey:     routeCfg.RateLimit.TierKey,
			DefaultTier: routeCfg.RateLimit.DefaultTier,
			KeyFn:       keyFn,
		})
	} else if routeCfg.RateLimit.Enabled || routeCfg.RateLimit.Rate > 0 {
		if routeCfg.RateLimit.Mode == "distributed" && g.redisClient != nil {
			s.rateLimiters.AddRouteDistributed(routeCfg.ID, ratelimit.RedisLimiterConfig{
				Client: g.redisClient,
				Prefix: "gw:rl:" + routeCfg.ID + ":",
				Rate:   routeCfg.RateLimit.Rate,
				Period: routeCfg.RateLimit.Period,
				Burst:  routeCfg.RateLimit.Burst,
				PerIP:  routeCfg.RateLimit.PerIP,
				Key:    routeCfg.RateLimit.Key,
			})
		} else if routeCfg.RateLimit.Algorithm == "sliding_window" {
			s.rateLimiters.AddRouteSlidingWindow(routeCfg.ID, ratelimit.Config{
				Rate:   routeCfg.RateLimit.Rate,
				Period: routeCfg.RateLimit.Period,
				Burst:  routeCfg.RateLimit.Burst,
				PerIP:  routeCfg.RateLimit.PerIP,
				Key:    routeCfg.RateLimit.Key,
			})
		} else {
			s.rateLimiters.AddRoute(routeCfg.ID, ratelimit.Config{
				Rate:   routeCfg.RateLimit.Rate,
				Period: routeCfg.RateLimit.Period,
				Burst:  routeCfg.RateLimit.Burst,
				PerIP:  routeCfg.RateLimit.PerIP,
				Key:    routeCfg.RateLimit.Key,
			})
		}
	}

	// gRPC handler
	if routeCfg.GRPC.Enabled {
		s.grpcHandlers.AddRoute(routeCfg.ID, routeCfg.GRPC)
	}

	// Protocol translator
	if routeCfg.Protocol.Type != "" {
		bal := s.routeProxies[routeCfg.ID].GetBalancer()
		if err := s.translators.AddRoute(routeCfg.ID, routeCfg.Protocol, bal); err != nil {
			return fmt.Errorf("protocol translator: route %s: %w", routeCfg.ID, err)
		}
	}

	// Generic features
	for _, f := range s.features {
		if err := f.Setup(routeCfg.ID, routeCfg); err != nil {
			return fmt.Errorf("feature %s: route %s: %w", f.Name(), routeCfg.ID, err)
		}
	}

	// Sequential handler (needs transport from proxy pool)
	if routeCfg.Sequential.Enabled {
		transport := g.proxy.GetTransportPool().Get(routeCfg.Upstream)
		if err := s.sequentialHandlers.AddRoute(routeCfg.ID, routeCfg.Sequential, transport); err != nil {
			return fmt.Errorf("sequential: route %s: %w", routeCfg.ID, err)
		}
	}

	// Aggregate handler (needs transport from proxy pool)
	if routeCfg.Aggregate.Enabled {
		transport := g.proxy.GetTransportPool().Get(routeCfg.Upstream)
		if err := s.aggregateHandlers.AddRoute(routeCfg.ID, routeCfg.Aggregate, transport); err != nil {
			return fmt.Errorf("aggregate: route %s: %w", routeCfg.ID, err)
		}
	}

	// Override per-try timeout with backend timeout when configured
	if routeCfg.TimeoutPolicy.Backend > 0 {
		s.routeProxies[routeCfg.ID].SetPerTryTimeout(routeCfg.TimeoutPolicy.Backend)
	}

	// Canary setup (needs WeightedBalancer)
	if routeCfg.Canary.Enabled {
		if wb, ok := s.routeProxies[routeCfg.ID].GetBalancer().(*loadbalancer.WeightedBalancer); ok {
			if err := s.canaryControllers.AddRoute(routeCfg.ID, routeCfg.Canary, wb); err != nil {
				return fmt.Errorf("canary: route %s: %w", routeCfg.ID, err)
			}
			if routeCfg.Canary.AutoStart {
				if ctrl := s.canaryControllers.GetController(routeCfg.ID); ctrl != nil {
					if err := ctrl.Start(); err != nil {
						return fmt.Errorf("canary auto-start: route %s: %w", routeCfg.ID, err)
					}
				}
			}
		}
	}

	// Blue-green setup (needs WeightedBalancer)
	if routeCfg.BlueGreen.Enabled {
		if wb, ok := s.routeProxies[routeCfg.ID].GetBalancer().(*loadbalancer.WeightedBalancer); ok {
			s.blueGreenControllers.AddRoute(routeCfg.ID, routeCfg.BlueGreen, wb, g.healthChecker)
		}
	}

	// A/B test setup (needs WeightedBalancer)
	if routeCfg.ABTest.Enabled {
		if wb, ok := s.routeProxies[routeCfg.ID].GetBalancer().(*loadbalancer.WeightedBalancer); ok {
			s.abTests.AddRoute(routeCfg.ID, routeCfg.ABTest, wb)
		}
	}

	// Outlier detection setup (needs Balancer)
	if routeCfg.OutlierDetection.Enabled {
		s.outlierDetectors.AddRoute(routeCfg.ID, routeCfg.OutlierDetection, s.routeProxies[routeCfg.ID].GetBalancer())
	}

	// Backpressure setup (needs Balancer)
	if routeCfg.Backpressure.Enabled {
		if rp := s.routeProxies[routeCfg.ID]; rp != nil {
			s.backpressureHandlers.AddRoute(routeCfg.ID, routeCfg.Backpressure, rp.GetBalancer())
		}
	}

	// Build middleware pipeline - we need a temporary gateway-like context
	handler := g.buildRouteHandlerForState(s, routeCfg.ID, routeCfg, route, s.routeProxies[routeCfg.ID])
	s.routeHandlers[routeCfg.ID] = handler

	return nil
}

// watchServiceForState is like watchService but writes to a gatewayState's routeProxies.
func (g *Gateway) watchServiceForState(s *gatewayState, ctx context.Context, routeID, serviceName string, tags []string) {
	ch, err := g.registry.Watch(ctx, serviceName)
	if err != nil {
		logging.Error("Failed to watch service during reload",
			zap.String("service", serviceName),
			zap.Error(err),
		)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case services, ok := <-ch:
			if !ok {
				return
			}

			var filtered []*registry.Service
			for _, svc := range services {
				if hasAllTags(svc.Tags, tags) {
					filtered = append(filtered, svc)
				}
			}

			var backends []*loadbalancer.Backend
			for _, svc := range filtered {
				be := &loadbalancer.Backend{
					URL:     svc.URL(),
					Weight:  1,
					Healthy: svc.Health == registry.HealthPassing,
				}
				be.InitParsedURL()
				backends = append(backends, be)
			}

			// The state's routeProxies are accessed by the Gateway under g.mu,
			// but since this watcher was started for the new state it's safe to
			// read from it directly — the map doesn't change after buildState returns.
			if rp, ok := s.routeProxies[routeID]; ok {
				rp.UpdateBackends(backends)
			}
		}
	}
}

// buildRouteHandlerForState is like buildRouteHandler but reads from a gatewayState.
func (g *Gateway) buildRouteHandlerForState(s *gatewayState, routeID string, cfg config.RouteConfig, route *router.Route, rp *proxy.RouteProxy) http.Handler {
	// We temporarily swap the Gateway's fields to build the handler, then swap back.
	// This is safe because buildState is called without holding g.mu.
	// Since buildRouteHandler reads from g's field directly, we do a field-by-field swap.
	// Alternative: refactor buildRouteHandler to take a state param. But to minimize diff,
	// we reuse the existing method by pointing g's fields at the new state's managers.
	//
	// However, this approach is fragile. Instead, let's just call buildRouteHandler but
	// override the fields that differ. Since we can't hold both old and new state,
	// we pass through the real method after temporarily installing state managers.

	// Save old state
	oldIPFilters := g.ipFilters
	oldGlobalIPFilter := g.globalIPFilter
	oldCorsHandlers := g.corsHandlers
	oldRateLimiters := g.rateLimiters
	oldThrottlers := g.throttlers
	oldPriorityAdmitter := g.priorityAdmitter
	oldPriorityConfigs := g.priorityConfigs
	oldGlobalRules := g.globalRules
	oldRouteRules := g.routeRules
	oldWafHandlers := g.wafHandlers
	oldFaultInjectors := g.faultInjectors
	oldBandwidthLimiters := g.bandwidthLimiters
	oldValidators := g.validators
	oldCaches := g.caches
	oldCircuitBreakers := g.circuitBreakers
	oldCompressors := g.compressors
	oldMirrors := g.mirrors
	oldGrpcHandlers := g.grpcHandlers
	oldTranslators := g.translators
	oldGraphqlParsers := g.graphqlParsers
	oldCoalescers := g.coalescers
	oldCanaryControllers := g.canaryControllers
	oldAdaptiveLimiters := g.adaptiveLimiters
	oldExtAuths := g.extAuths
	oldVersioners := g.versioners
	oldAccessLogConfigs := g.accessLogConfigs
	oldOpenAPIValidators := g.openapiValidators
	oldTimeoutConfigs := g.timeoutConfigs
	oldErrorPages := g.errorPages
	oldNonceCheckers := g.nonceCheckers
	oldOutlierDetectors := g.outlierDetectors
	oldIdempotencyHandlers := g.idempotencyHandlers
	oldBackendSigners := g.backendSigners
	oldDecompressors := g.decompressors
	oldResponseLimiters := g.responseLimiters
	oldSecurityHeaders := g.securityHeaders
	oldMaintenanceHandlers := g.maintenanceHandlers
	oldBotDetectors := g.botDetectors
	oldProxyRateLimiters := g.proxyRateLimiters
	oldMockHandlers := g.mockHandlers
	oldClaimsPropagators := g.claimsPropagators
	oldBackendAuths := g.backendAuths
	oldStatusMappers := g.statusMappers
	oldStaticFiles := g.staticFiles
	oldSpikeArresters := g.spikeArresters
	oldContentReplacers := g.contentReplacers
	oldBodyGenerators := g.bodyGenerators
	oldQuotaEnforcers := g.quotaEnforcers
	oldSequentialHandlers := g.sequentialHandlers
	oldAggregateHandlers := g.aggregateHandlers
	oldRespBodyGenerators := g.respBodyGenerators
	oldParamForwarders := g.paramForwarders
	oldContentNegotiators := g.contentNegotiators
	oldCdnHeaders := g.cdnHeaders
	oldBackendEncoders := g.backendEncoders
	oldSSEHandlers := g.sseHandlers
	oldRealIPExtractor := g.realIPExtractor
	oldTokenChecker := g.tokenChecker
	oldInboundVerifiers := g.inboundVerifiers
	oldPIIRedactors := g.piiRedactors
	oldFieldEncryptors := g.fieldEncryptors
	oldBlueGreenControllers := g.blueGreenControllers
	oldABTests := g.abTests
	oldRequestQueues := g.requestQueues
	oldBaggagePropagators := g.baggagePropagators
	oldBackpressureHandlers2 := g.backpressureHandlers
	oldAuditLoggers2 := g.auditLoggers
	oldTenantManager := g.tenantManager

	// Install new state
	g.ipFilters = s.ipFilters
	g.globalIPFilter = s.globalIPFilter
	g.corsHandlers = s.corsHandlers
	g.rateLimiters = s.rateLimiters
	g.throttlers = s.throttlers
	g.priorityAdmitter = s.priorityAdmitter
	g.priorityConfigs = s.priorityConfigs
	g.globalRules = s.globalRules
	g.routeRules = s.routeRules
	g.wafHandlers = s.wafHandlers
	g.faultInjectors = s.faultInjectors
	g.bandwidthLimiters = s.bandwidthLimiters
	g.validators = s.validators
	g.caches = s.caches
	g.circuitBreakers = s.circuitBreakers
	g.compressors = s.compressors
	g.mirrors = s.mirrors
	g.grpcHandlers = s.grpcHandlers
	g.translators = s.translators
	g.graphqlParsers = s.graphqlParsers
	g.coalescers = s.coalescers
	g.canaryControllers = s.canaryControllers
	g.adaptiveLimiters = s.adaptiveLimiters
	g.extAuths = s.extAuths
	g.versioners = s.versioners
	g.accessLogConfigs = s.accessLogConfigs
	g.openapiValidators = s.openapiValidators
	g.timeoutConfigs = s.timeoutConfigs
	g.errorPages = s.errorPages
	g.nonceCheckers = s.nonceCheckers
	g.outlierDetectors = s.outlierDetectors
	g.idempotencyHandlers = s.idempotencyHandlers
	g.backendSigners = s.backendSigners
	g.decompressors = s.decompressors
	g.responseLimiters = s.responseLimiters
	g.securityHeaders = s.securityHeaders
	g.maintenanceHandlers = s.maintenanceHandlers
	g.botDetectors = s.botDetectors
	g.proxyRateLimiters = s.proxyRateLimiters
	g.mockHandlers = s.mockHandlers
	g.claimsPropagators = s.claimsPropagators
	g.backendAuths = s.backendAuths
	g.statusMappers = s.statusMappers
	g.staticFiles = s.staticFiles
	g.spikeArresters = s.spikeArresters
	g.contentReplacers = s.contentReplacers
	g.bodyGenerators = s.bodyGenerators
	g.quotaEnforcers = s.quotaEnforcers
	g.sequentialHandlers = s.sequentialHandlers
	g.aggregateHandlers = s.aggregateHandlers
	g.respBodyGenerators = s.respBodyGenerators
	g.paramForwarders = s.paramForwarders
	g.contentNegotiators = s.contentNegotiators
	g.cdnHeaders = s.cdnHeaders
	g.backendEncoders = s.backendEncoders
	g.sseHandlers = s.sseHandlers
	g.realIPExtractor = s.realIPExtractor
	g.tokenChecker = s.tokenChecker
	g.inboundVerifiers = s.inboundVerifiers
	g.piiRedactors = s.piiRedactors
	g.fieldEncryptors = s.fieldEncryptors
	g.blueGreenControllers = s.blueGreenControllers
	g.abTests = s.abTests
	g.requestQueues = s.requestQueues
	g.baggagePropagators = s.baggagePropagators
	g.backpressureHandlers = s.backpressureHandlers
	g.auditLoggers = s.auditLoggers
	g.tenantManager = s.tenantManager

	handler := g.buildRouteHandler(routeID, cfg, route, rp)

	// Restore old state
	g.ipFilters = oldIPFilters
	g.globalIPFilter = oldGlobalIPFilter
	g.corsHandlers = oldCorsHandlers
	g.rateLimiters = oldRateLimiters
	g.throttlers = oldThrottlers
	g.priorityAdmitter = oldPriorityAdmitter
	g.priorityConfigs = oldPriorityConfigs
	g.globalRules = oldGlobalRules
	g.routeRules = oldRouteRules
	g.wafHandlers = oldWafHandlers
	g.faultInjectors = oldFaultInjectors
	g.bandwidthLimiters = oldBandwidthLimiters
	g.validators = oldValidators
	g.caches = oldCaches
	g.circuitBreakers = oldCircuitBreakers
	g.compressors = oldCompressors
	g.mirrors = oldMirrors
	g.grpcHandlers = oldGrpcHandlers
	g.translators = oldTranslators
	g.graphqlParsers = oldGraphqlParsers
	g.coalescers = oldCoalescers
	g.canaryControllers = oldCanaryControllers
	g.adaptiveLimiters = oldAdaptiveLimiters
	g.extAuths = oldExtAuths
	g.versioners = oldVersioners
	g.accessLogConfigs = oldAccessLogConfigs
	g.openapiValidators = oldOpenAPIValidators
	g.timeoutConfigs = oldTimeoutConfigs
	g.errorPages = oldErrorPages
	g.nonceCheckers = oldNonceCheckers
	g.outlierDetectors = oldOutlierDetectors
	g.idempotencyHandlers = oldIdempotencyHandlers
	g.backendSigners = oldBackendSigners
	g.decompressors = oldDecompressors
	g.responseLimiters = oldResponseLimiters
	g.securityHeaders = oldSecurityHeaders
	g.maintenanceHandlers = oldMaintenanceHandlers
	g.botDetectors = oldBotDetectors
	g.proxyRateLimiters = oldProxyRateLimiters
	g.mockHandlers = oldMockHandlers
	g.claimsPropagators = oldClaimsPropagators
	g.backendAuths = oldBackendAuths
	g.statusMappers = oldStatusMappers
	g.staticFiles = oldStaticFiles
	g.spikeArresters = oldSpikeArresters
	g.contentReplacers = oldContentReplacers
	g.bodyGenerators = oldBodyGenerators
	g.quotaEnforcers = oldQuotaEnforcers
	g.sequentialHandlers = oldSequentialHandlers
	g.aggregateHandlers = oldAggregateHandlers
	g.respBodyGenerators = oldRespBodyGenerators
	g.paramForwarders = oldParamForwarders
	g.contentNegotiators = oldContentNegotiators
	g.cdnHeaders = oldCdnHeaders
	g.backendEncoders = oldBackendEncoders
	g.sseHandlers = oldSSEHandlers
	g.realIPExtractor = oldRealIPExtractor
	g.tokenChecker = oldTokenChecker
	g.inboundVerifiers = oldInboundVerifiers
	g.piiRedactors = oldPIIRedactors
	g.fieldEncryptors = oldFieldEncryptors
	g.blueGreenControllers = oldBlueGreenControllers
	g.abTests = oldABTests
	g.requestQueues = oldRequestQueues
	g.baggagePropagators = oldBaggagePropagators
	g.backpressureHandlers = oldBackpressureHandlers2
	g.auditLoggers = oldAuditLoggers2
	g.tenantManager = oldTenantManager

	return handler
}

// Reload atomically replaces all route-scoped state with a new config.
// Shared infrastructure (proxy, healthChecker, registry, metricsCollector, redisClient, tracer) is preserved.
// In-flight requests complete with the old handler (handler refs are grabbed under RLock).
func (g *Gateway) Reload(newCfg *config.Config) ReloadResult {
	result := ReloadResult{Timestamp: time.Now()}

	// Build new state (no locks held)
	newState, err := g.buildState(newCfg)
	if err != nil {
		result.Error = err.Error()
		if g.webhookDispatcher != nil {
			g.webhookDispatcher.Emit(webhook.NewEvent(webhook.ConfigReloadFailure, "", map[string]interface{}{
				"error": err.Error(),
			}))
		}
		return result
	}

	// Compute changes
	result.Changes = diffConfig(g.config, newCfg)

	// Save old state for cleanup
	oldConfig := g.config
	_ = oldConfig
	oldWatchCancels := g.watchCancels
	oldTranslators := g.translators
	oldJWT := g.jwtAuth
	oldCanaryControllers := g.canaryControllers
	oldBlueGreenControllers := g.blueGreenControllers
	oldAdaptiveLimiters := g.adaptiveLimiters
	oldExtAuths := g.extAuths
	oldNonceCheckers := g.nonceCheckers
	oldOutlierDetectors := g.outlierDetectors
	oldIdempotencyHandlers := g.idempotencyHandlers
	oldBackendSigners := g.backendSigners
	oldTokenChecker := g.tokenChecker
	oldQuotaEnforcers := g.quotaEnforcers
	oldLoadShedder := g.loadShedder
	oldBackpressureHandlers := g.backpressureHandlers
	oldAuditLoggers := g.auditLoggers

	// Swap all state under write lock
	g.mu.Lock()
	g.config = newState.config
	g.router = newState.router
	g.routeProxies.Store(&newState.routeProxies)
	g.routeHandlers.Store(&newState.routeHandlers)
	g.watchCancels = newState.watchCancels
	g.features = newState.features
	g.circuitBreakers = newState.circuitBreakers
	g.caches = newState.caches
	g.ipFilters = newState.ipFilters
	g.globalIPFilter = newState.globalIPFilter
	g.corsHandlers = newState.corsHandlers
	g.compressors = newState.compressors
	g.validators = newState.validators
	g.mirrors = newState.mirrors
	g.routeRules = newState.routeRules
	g.globalRules = newState.globalRules
	g.throttlers = newState.throttlers
	g.bandwidthLimiters = newState.bandwidthLimiters
	g.priorityAdmitter = newState.priorityAdmitter
	g.priorityConfigs = newState.priorityConfigs
	g.faultInjectors = newState.faultInjectors
	g.wafHandlers = newState.wafHandlers
	g.graphqlParsers = newState.graphqlParsers
	g.coalescers = newState.coalescers
	g.canaryControllers = newState.canaryControllers
	g.adaptiveLimiters = newState.adaptiveLimiters
	g.extAuths = newState.extAuths
	g.versioners = newState.versioners
	g.accessLogConfigs = newState.accessLogConfigs
	g.openapiValidators = newState.openapiValidators
	g.timeoutConfigs = newState.timeoutConfigs
	g.errorPages = newState.errorPages
	g.nonceCheckers = newState.nonceCheckers
	g.csrfProtectors = newState.csrfProtectors
	g.outlierDetectors = newState.outlierDetectors
	g.idempotencyHandlers = newState.idempotencyHandlers
	g.backendSigners = newState.backendSigners
	g.decompressors = newState.decompressors
	g.responseLimiters = newState.responseLimiters
	g.securityHeaders = newState.securityHeaders
	g.maintenanceHandlers = newState.maintenanceHandlers
	g.botDetectors = newState.botDetectors
	g.proxyRateLimiters = newState.proxyRateLimiters
	g.mockHandlers = newState.mockHandlers
	g.claimsPropagators = newState.claimsPropagators
	g.backendAuths = newState.backendAuths
	g.statusMappers = newState.statusMappers
	g.staticFiles = newState.staticFiles
	g.spikeArresters = newState.spikeArresters
	g.contentReplacers = newState.contentReplacers
	g.bodyGenerators = newState.bodyGenerators
	g.quotaEnforcers = newState.quotaEnforcers
	g.sequentialHandlers = newState.sequentialHandlers
	g.aggregateHandlers = newState.aggregateHandlers
	g.respBodyGenerators = newState.respBodyGenerators
	g.paramForwarders = newState.paramForwarders
	g.contentNegotiators = newState.contentNegotiators
	g.sseHandlers = newState.sseHandlers
	g.realIPExtractor = newState.realIPExtractor
	g.tokenChecker = newState.tokenChecker
	g.translators = newState.translators
	g.rateLimiters = newState.rateLimiters
	g.grpcHandlers = newState.grpcHandlers
	g.budgetPools = newState.budgetPools
	g.baggagePropagators = newState.baggagePropagators
	g.backpressureHandlers = newState.backpressureHandlers
	g.auditLoggers = newState.auditLoggers
	g.inboundVerifiers = newState.inboundVerifiers
	g.piiRedactors = newState.piiRedactors
	g.fieldEncryptors = newState.fieldEncryptors
	g.blueGreenControllers = newState.blueGreenControllers
	g.abTests = newState.abTests
	g.requestQueues = newState.requestQueues
	g.apiKeyAuth = newState.apiKeyAuth
	g.jwtAuth = newState.jwtAuth
	g.oauthAuth = newState.oauthAuth
	// Swap tenant manager (close old one's quota goroutines)
	oldTenantMgr := g.tenantManager
	g.tenantManager = newState.tenantManager
	if oldTenantMgr != nil {
		oldTenantMgr.Close()
	}
	// Rebuild global singletons from new config
	if newCfg.ServiceRateLimit.Enabled {
		g.serviceLimiter = serviceratelimit.New(newCfg.ServiceRateLimit)
	} else {
		g.serviceLimiter = nil
	}
	if newCfg.DebugEndpoint.Enabled {
		g.debugHandler = debug.New(newCfg.DebugEndpoint, newCfg)
	} else {
		g.debugHandler = nil
	}
	// Rebuild HTTPS redirect and allowed hosts from new config
	if newCfg.HTTPSRedirect.Enabled {
		g.httpsRedirect = httpsredirect.New(newCfg.HTTPSRedirect)
	} else {
		g.httpsRedirect = nil
	}
	if newCfg.AllowedHosts.Enabled {
		g.allowedHosts = allowedhosts.New(newCfg.AllowedHosts)
	} else {
		g.allowedHosts = nil
	}
	if newCfg.LoadShedding.Enabled {
		g.loadShedder = loadshed.New(newCfg.LoadShedding)
	} else {
		g.loadShedder = nil
	}
	g.mu.Unlock()

	// Clean up old state (after releasing lock — in-flight requests already hold handler refs)
	for _, cancel := range oldWatchCancels {
		cancel()
	}
	oldTranslators.Close()
	oldExtAuths.CloseAll()
	oldCanaryControllers.StopAll()
	oldBlueGreenControllers.StopAll()
	oldAdaptiveLimiters.StopAll()
	oldNonceCheckers.CloseAll()
	oldOutlierDetectors.StopAll()
	oldIdempotencyHandlers.CloseAll()
	_ = oldBackendSigners // no cleanup needed (stateless)
	oldQuotaEnforcers.CloseAll()
	oldBackpressureHandlers.CloseAll()
	oldAuditLoggers.CloseAll()
	if oldLoadShedder != nil {
		oldLoadShedder.Close()
	}
	if oldTokenChecker != nil {
		oldTokenChecker.Close()
	}
	if oldJWT != nil {
		oldJWT.Close()
	}

	// Reconcile health checker: remove backends no longer present
	newBackendURLs := make(map[string]bool)
	// Collect backend URLs from upstreams
	for _, us := range newCfg.Upstreams {
		for _, b := range us.Backends {
			newBackendURLs[b.URL] = true
		}
	}
	for _, routeCfg := range newCfg.Routes {
		// Resolve upstream refs to find effective backends
		resolved := resolveUpstreamRefs(newCfg, routeCfg)
		for _, b := range resolved.Backends {
			newBackendURLs[b.URL] = true
		}
		for _, split := range resolved.TrafficSplit {
			for _, b := range split.Backends {
				newBackendURLs[b.URL] = true
			}
		}
		if resolved.Versioning.Enabled {
			for _, vcfg := range resolved.Versioning.Versions {
				for _, b := range vcfg.Backends {
					newBackendURLs[b.URL] = true
				}
			}
		}
		if resolved.Mirror.Enabled {
			for _, b := range resolved.Mirror.Backends {
				newBackendURLs[b.URL] = true
			}
		}
	}
	for url := range g.healthChecker.GetAllStatus() {
		if !newBackendURLs[url] {
			g.healthChecker.RemoveBackend(url)
		}
	}

	// Update webhook endpoints and emit success event
	if g.webhookDispatcher != nil {
		g.webhookDispatcher.UpdateEndpoints(newCfg.Webhooks.Endpoints)
		g.webhookDispatcher.Emit(webhook.NewEvent(webhook.ConfigReloadSuccess, "", map[string]interface{}{
			"changes": result.Changes,
		}))
	}

	result.Success = true
	return result
}

// diffConfig returns a list of human-readable changes between old and new configs.
func diffConfig(oldCfg, newCfg *config.Config) []string {
	var changes []string

	oldRoutes := make(map[string]bool, len(oldCfg.Routes))
	for _, r := range oldCfg.Routes {
		oldRoutes[r.ID] = true
	}
	newRoutes := make(map[string]bool, len(newCfg.Routes))
	for _, r := range newCfg.Routes {
		newRoutes[r.ID] = true
	}

	// Added routes
	for id := range newRoutes {
		if !oldRoutes[id] {
			changes = append(changes, fmt.Sprintf("route added: %s", id))
		}
	}
	// Removed routes
	for id := range oldRoutes {
		if !newRoutes[id] {
			changes = append(changes, fmt.Sprintf("route removed: %s", id))
		}
	}
	// Modified routes (in both old and new)
	for id := range newRoutes {
		if oldRoutes[id] {
			changes = append(changes, fmt.Sprintf("route reloaded: %s", id))
		}
	}

	// Listener changes
	if len(oldCfg.Listeners) != len(newCfg.Listeners) {
		changes = append(changes, fmt.Sprintf("listeners changed: %d -> %d", len(oldCfg.Listeners), len(newCfg.Listeners)))
	}

	sort.Strings(changes)
	return changes
}

// wsProxy accessor for buildState — uses shared wsProxy from Gateway
func (g *Gateway) getWSProxy() *websocket.Proxy {
	return g.wsProxy
}

// resolver accessor for buildState
func (g *Gateway) getResolver() *variables.Resolver {
	return g.resolver
}

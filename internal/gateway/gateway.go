package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/logging"
	"go.uber.org/zap"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/gateway/internal/cache"
	"github.com/wudi/gateway/internal/canary"
	"github.com/wudi/gateway/internal/circuitbreaker"
	"github.com/wudi/gateway/internal/coalesce"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/errors"
	"github.com/wudi/gateway/internal/graphql"
	"github.com/wudi/gateway/internal/health"
	"github.com/wudi/gateway/internal/loadbalancer"
	"github.com/wudi/gateway/internal/loadbalancer/outlier"
	"github.com/wudi/gateway/internal/metrics"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/internal/middleware/accesslog"
	"github.com/wudi/gateway/internal/middleware/allowedhosts"
	"github.com/wudi/gateway/internal/middleware/altsvc"
	"github.com/wudi/gateway/internal/middleware/auth"
	"github.com/wudi/gateway/internal/middleware/auditlog"
	"github.com/wudi/gateway/internal/middleware/backendauth"
	"github.com/wudi/gateway/internal/middleware/backpressure"
	"github.com/wudi/gateway/internal/middleware/baggage"
	"github.com/wudi/gateway/internal/middleware/backendenc"
	"github.com/wudi/gateway/internal/middleware/bodygen"
	"github.com/wudi/gateway/internal/middleware/botdetect"
	"github.com/wudi/gateway/internal/middleware/clientmtls"
	"github.com/wudi/gateway/internal/middleware/cdnheaders"
	"github.com/wudi/gateway/internal/middleware/claimsprop"
	"github.com/wudi/gateway/internal/middleware/tokenexchange"
	"github.com/wudi/gateway/internal/middleware/compression"
	"github.com/wudi/gateway/internal/middleware/contentreplacer"
	"github.com/wudi/gateway/internal/middleware/cors"
	"github.com/wudi/gateway/internal/middleware/debug"
	"github.com/wudi/gateway/internal/middleware/decompress"
	"github.com/wudi/gateway/internal/middleware/csrf"
	"github.com/wudi/gateway/internal/middleware/errorpages"
	"github.com/wudi/gateway/internal/middleware/extauth"
	"github.com/wudi/gateway/internal/middleware/httpsredirect"
	"github.com/wudi/gateway/internal/middleware/geo"
	"github.com/wudi/gateway/internal/middleware/dedup"
	"github.com/wudi/gateway/internal/middleware/idempotency"
	"github.com/wudi/gateway/internal/middleware/ipblocklist"
	"github.com/wudi/gateway/internal/middleware/ipfilter"
	"github.com/wudi/gateway/internal/middleware/loadshed"
	"github.com/wudi/gateway/internal/middleware/mtls"
	"github.com/wudi/gateway/internal/middleware/nonce"
	"github.com/wudi/gateway/internal/middleware/maintenance"
	"github.com/wudi/gateway/internal/middleware/mock"
	"github.com/wudi/gateway/internal/middleware/proxyratelimit"
	"github.com/wudi/gateway/internal/middleware/quota"
	"github.com/wudi/gateway/internal/middleware/tenant"
	"github.com/wudi/gateway/internal/middleware/realip"
	"github.com/wudi/gateway/internal/middleware/securityheaders"
	"github.com/wudi/gateway/internal/middleware/serviceratelimit"
	"github.com/wudi/gateway/internal/middleware/sse"
	"github.com/wudi/gateway/internal/middleware/signing"
	"github.com/wudi/gateway/internal/middleware/ssrf"
	"github.com/wudi/gateway/internal/middleware/spikearrest"
	"github.com/wudi/gateway/internal/middleware/staticfiles"
	"github.com/wudi/gateway/internal/middleware/statusmap"
	openapivalidation "github.com/wudi/gateway/internal/middleware/openapi"
	"github.com/wudi/gateway/internal/middleware/ratelimit"
	"github.com/wudi/gateway/internal/middleware/responselimit"
	"github.com/wudi/gateway/internal/middleware/timeout"
	"github.com/wudi/gateway/internal/middleware/tokenrevoke"
	"github.com/wudi/gateway/internal/middleware/transform"
	"github.com/wudi/gateway/internal/middleware/validation"
	"github.com/wudi/gateway/internal/middleware/versioning"
	"github.com/wudi/gateway/internal/middleware/waf"
	"github.com/wudi/gateway/internal/mirror"
	"github.com/wudi/gateway/internal/proxy"
	fastcgiproxy "github.com/wudi/gateway/internal/proxy/fastcgi"
	grpcproxy "github.com/wudi/gateway/internal/proxy/grpc"
	"github.com/wudi/gateway/internal/proxy/protocol"
	_ "github.com/wudi/gateway/internal/proxy/protocol/graphql"
	_ "github.com/wudi/gateway/internal/proxy/protocol/soap"
	"github.com/wudi/gateway/internal/abtest"
	"github.com/wudi/gateway/internal/bluegreen"
	"github.com/wudi/gateway/internal/middleware/contentneg"
	"github.com/wudi/gateway/internal/middleware/errorhandling"
	"github.com/wudi/gateway/internal/middleware/fieldencrypt"
	"github.com/wudi/gateway/internal/middleware/fieldreplacer"
	"github.com/wudi/gateway/internal/middleware/inboundsigning"
	"github.com/wudi/gateway/internal/middleware/jmespath"
	"github.com/wudi/gateway/internal/middleware/luascript"
	wasmPlugin "github.com/wudi/gateway/internal/middleware/wasm"
	"github.com/wudi/gateway/internal/middleware/modifiers"
	"github.com/wudi/gateway/internal/middleware/paramforward"
	"github.com/wudi/gateway/internal/middleware/requestqueue"
	"github.com/wudi/gateway/internal/middleware/piiredact"
	"github.com/wudi/gateway/internal/middleware/respbodygen"
	"github.com/wudi/gateway/internal/proxy/aggregate"
	lambdaproxy "github.com/wudi/gateway/internal/proxy/lambda"
	amqpproxy "github.com/wudi/gateway/internal/proxy/amqp"
	pubsubproxy "github.com/wudi/gateway/internal/proxy/pubsub"
	"github.com/wudi/gateway/internal/proxy/sequential"
	"github.com/wudi/gateway/internal/registry"
	"github.com/wudi/gateway/internal/registry/consul"
	dnsregistry "github.com/wudi/gateway/internal/registry/dns"
	"github.com/wudi/gateway/internal/registry/etcd"
	"github.com/wudi/gateway/internal/registry/memory"
	"github.com/wudi/gateway/internal/retry"
	"github.com/wudi/gateway/internal/router"
	"github.com/wudi/gateway/internal/rules"
	"github.com/wudi/gateway/internal/tracing"
	"github.com/wudi/gateway/internal/trafficreplay"
	"github.com/wudi/gateway/internal/trafficshape"
	"github.com/wudi/gateway/internal/variables"
	"github.com/wudi/gateway/internal/webhook"
	"github.com/wudi/gateway/internal/websocket"
)

// Gateway is the main API gateway
type Gateway struct {
	// Hot path — read on every request via serveHTTP (own cache line)
	routeHandlers atomic.Pointer[map[string]http.Handler]
	routeProxies  atomic.Pointer[map[string]*proxy.RouteProxy]
	_pad          [64 - 2*8]byte // prevent false sharing with cold fields below

	config        *config.Config
	router        *router.Router
	proxy         *proxy.Proxy
	registry      registry.Registry
	healthChecker *health.Checker
	apiKeyAuth    *auth.APIKeyAuth
	jwtAuth       *auth.JWTAuth
	oauthAuth     *auth.OAuthAuth
	rateLimiters  *ratelimit.RateLimitByRoute
	resolver      *variables.Resolver

	// New feature managers
	circuitBreakers *circuitbreaker.BreakerByRoute
	caches          *cache.CacheByRoute
	wsProxy         *websocket.Proxy

	// Feature managers (Batch 1-3)
	ipFilters        *ipfilter.IPFilterByRoute
	globalIPFilter   *ipfilter.Filter
	corsHandlers     *cors.CORSByRoute
	compressors      *compression.CompressorByRoute
	metricsCollector *metrics.Collector
	validators       *validation.ValidatorByRoute
	mirrors          *mirror.MirrorByRoute
	tracer           *tracing.Tracer

	grpcHandlers *grpcproxy.GRPCByRoute
	translators  *protocol.TranslatorByRoute

	globalRules *rules.RuleEngine
	routeRules  *rules.RulesByRoute

	// Traffic shaping managers
	throttlers        *trafficshape.ThrottleByRoute
	bandwidthLimiters *trafficshape.BandwidthByRoute
	priorityAdmitter  *trafficshape.PriorityAdmitter // shared across routes, nil if disabled
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
	csrfProtectors    *csrf.CSRFByRoute
	outlierDetectors  *outlier.DetectorByRoute
	geoFilters          *geo.GeoByRoute
	geoProvider         geo.Provider
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
	tokenExchangers     *tokenexchange.TokenExchangeByRoute
	backendAuths        *backendauth.BackendAuthByRoute
	statusMappers       *statusmap.StatusMapByRoute
	staticFiles         *staticfiles.StaticByRoute
	fastcgiHandlers     *fastcgiproxy.FastCGIByRoute
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
	serviceLimiter      *serviceratelimit.ServiceLimiter
	debugHandler        *debug.Handler
	realIPExtractor     *realip.CompiledRealIP
	httpsRedirect       *httpsredirect.CompiledHTTPSRedirect
	allowedHosts        *allowedhosts.CompiledAllowedHosts
	tokenChecker        *tokenrevoke.TokenChecker
	globalGeo         *geo.CompiledGeo
	webhookDispatcher *webhook.Dispatcher
	inboundVerifiers     *inboundsigning.InboundSigningByRoute
	piiRedactors         *piiredact.PIIRedactByRoute
	fieldEncryptors      *fieldencrypt.FieldEncryptByRoute
	blueGreenControllers *bluegreen.BlueGreenByRoute
	abTests              *abtest.ABTestByRoute
	requestQueues        *requestqueue.RequestQueueByRoute
	dedupHandlers        *dedup.DedupByRoute
	ipBlocklists         *ipblocklist.BlocklistByRoute
	clientMTLSVerifiers  *clientmtls.ClientMTLSByRoute
	globalBlocklist      *ipblocklist.Blocklist
	ssrfDialer           *ssrf.SafeDialer
	http3AltSvcPort      string // port for Alt-Svc header; empty = no HTTP/3
	budgetPools          map[string]*retry.Budget
	loadShedder          *loadshed.LoadShedder
	baggagePropagators   *baggage.BaggageByRoute
	backpressureHandlers *backpressure.BackpressureByRoute
	auditLoggers         *auditlog.AuditLogByRoute
	modifierChains       *modifiers.ModifiersByRoute
	jmespathHandlers     *jmespath.JMESPathByRoute
	fieldReplacers       *fieldreplacer.FieldReplacerByRoute
	errorHandlers        *errorhandling.ErrorHandlerByRoute
	luaScripters         *luascript.LuaScriptByRoute
	wasmPlugins          *wasmPlugin.WasmByRoute
	lambdaHandlers       *lambdaproxy.LambdaByRoute
	amqpHandlers         *amqpproxy.AMQPByRoute
	pubsubHandlers       *pubsubproxy.PubSubByRoute
	trafficReplay        *trafficreplay.ReplayByRoute

	tenantManager *tenant.Manager

	features []Feature

	redisClient *redis.Client // shared Redis client for distributed features

	watchCancels map[string]context.CancelFunc
	mu           sync.RWMutex // cold: only held during route add/reload
}

// storeRouteProxy atomically stores a route proxy using copy-on-write.
func (g *Gateway) storeRouteProxy(id string, rp *proxy.RouteProxy) {
	old := *g.routeProxies.Load()
	m := make(map[string]*proxy.RouteProxy, len(old)+1)
	for k, v := range old {
		m[k] = v
	}
	m[id] = rp
	g.routeProxies.Store(&m)
}

// storeRouteHandler atomically stores a route handler using copy-on-write.
func (g *Gateway) storeRouteHandler(id string, h http.Handler) {
	old := *g.routeHandlers.Load()
	m := make(map[string]http.Handler, len(old)+1)
	for k, v := range old {
		m[k] = v
	}
	m[id] = h
	g.routeHandlers.Store(&m)
}

// statusRecorder wraps http.ResponseWriter to capture the status code
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

var statusRecorderPool = sync.Pool{
	New: func() any {
		return &statusRecorder{}
	},
}

func getStatusRecorder(w http.ResponseWriter) *statusRecorder {
	rec := statusRecorderPool.Get().(*statusRecorder)
	rec.ResponseWriter = w
	rec.statusCode = 200
	return rec
}

func putStatusRecorder(rec *statusRecorder) {
	rec.ResponseWriter = nil
	statusRecorderPool.Put(rec)
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

// StatusCode implements StatusCapture.
func (sr *statusRecorder) StatusCode() int {
	return sr.statusCode
}

// Flush implements http.Flusher, forwarding to the underlying ResponseWriter if supported.
func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// New creates a new gateway
func New(cfg *config.Config) (*Gateway, error) {
	g := &Gateway{
		config:            cfg,
		router:            router.New(),
		rateLimiters:      ratelimit.NewRateLimitByRoute(),
		resolver:          variables.NewResolver(),
		circuitBreakers:   circuitbreaker.NewBreakerByRoute(),
		caches:            cache.NewCacheByRoute(nil),
		wsProxy:           websocket.NewProxy(config.WebSocketConfig{}),
		ipFilters:         ipfilter.NewIPFilterByRoute(),
		corsHandlers:      cors.NewCORSByRoute(),
		compressors:       compression.NewCompressorByRoute(),
		metricsCollector:  metrics.NewCollector(),
		validators:        validation.NewValidatorByRoute(),
		mirrors:           mirror.NewMirrorByRoute(),
		grpcHandlers:      grpcproxy.NewGRPCByRoute(),
		translators:       protocol.NewTranslatorByRoute(),
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
		csrfProtectors:    csrf.NewCSRFByRoute(),
		outlierDetectors:  outlier.NewDetectorByRoute(),
		geoFilters:          geo.NewGeoByRoute(),
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
		tokenExchangers:     tokenexchange.NewTokenExchangeByRoute(),
		backendAuths:        backendauth.NewBackendAuthByRoute(),
		statusMappers:       statusmap.NewStatusMapByRoute(),
		staticFiles:         staticfiles.NewStaticByRoute(),
		fastcgiHandlers:     fastcgiproxy.NewFastCGIByRoute(),
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
		sseHandlers:          sse.NewSSEByRoute(),
		inboundVerifiers:     inboundsigning.NewInboundSigningByRoute(),
		piiRedactors:         piiredact.NewPIIRedactByRoute(),
		fieldEncryptors:      fieldencrypt.NewFieldEncryptByRoute(),
		blueGreenControllers: bluegreen.NewBlueGreenByRoute(),
		abTests:              abtest.NewABTestByRoute(),
		requestQueues:        requestqueue.NewRequestQueueByRoute(),
		dedupHandlers:        dedup.NewDedupByRoute(),
		ipBlocklists:         ipblocklist.NewBlocklistByRoute(),
		clientMTLSVerifiers:  clientmtls.NewClientMTLSByRoute(),
		budgetPools:          make(map[string]*retry.Budget),
		baggagePropagators:   baggage.NewBaggageByRoute(),
		backpressureHandlers: backpressure.NewBackpressureByRoute(),
		auditLoggers:         auditlog.NewAuditLogByRoute(),
		modifierChains:       modifiers.NewModifiersByRoute(),
		jmespathHandlers:     jmespath.NewJMESPathByRoute(),
		fieldReplacers:       fieldreplacer.NewFieldReplacerByRoute(),
		errorHandlers:        errorhandling.NewErrorHandlerByRoute(),
		luaScripters:         luascript.NewLuaScriptByRoute(),
		wasmPlugins:          wasmPlugin.NewWasmByRoute(cfg.Wasm),
		lambdaHandlers:       lambdaproxy.NewLambdaByRoute(),
		amqpHandlers:         amqpproxy.NewAMQPByRoute(),
		pubsubHandlers:       pubsubproxy.NewPubSubByRoute(),
		trafficReplay:        trafficreplay.NewReplayByRoute(),
		watchCancels:      make(map[string]context.CancelFunc),
	}

	// Initialize shared retry budget pools
	for name, bc := range cfg.RetryBudgets {
		g.budgetPools[name] = retry.NewBudget(bc.Ratio, bc.MinRetries, bc.Window)
	}

	// Initialize atomic pointers for hot-path map access
	rp := make(map[string]*proxy.RouteProxy)
	g.routeProxies.Store(&rp)
	rh := make(map[string]http.Handler)
	g.routeHandlers.Store(&rh)

	// Initialize shared priority admitter if global priority is enabled
	if cfg.TrafficShaping.Priority.Enabled {
		g.priorityAdmitter = trafficshape.NewPriorityAdmitter(cfg.TrafficShaping.Priority.MaxConcurrent)
	}

	// Initialize load shedder if enabled
	if cfg.LoadShedding.Enabled {
		g.loadShedder = loadshed.New(cfg.LoadShedding)
	}

	// Register features for generic setup iteration
	g.features = []Feature{
		// Simple features: isActive → extract → addRoute
		newFeature("ip_filter", "", func(id string, rc config.RouteConfig) error {
			if rc.IPFilter.Enabled {
				return g.ipFilters.AddRoute(id, rc.IPFilter)
			}
			return nil
		}, g.ipFilters.RouteIDs, nil),
		newFeature("cors", "", func(id string, rc config.RouteConfig) error {
			if rc.CORS.Enabled {
				return g.corsHandlers.AddRoute(id, rc.CORS)
			}
			return nil
		}, g.corsHandlers.RouteIDs, nil),
		newFeature("circuit_breaker", "/circuit-breakers", func(id string, rc config.RouteConfig) error {
			if rc.CircuitBreaker.Enabled {
				if rc.CircuitBreaker.Mode == "distributed" && g.redisClient != nil {
					g.circuitBreakers.AddRouteDistributed(id, rc.CircuitBreaker, g.redisClient)
				} else {
					g.circuitBreakers.AddRoute(id, rc.CircuitBreaker)
				}
			}
			return nil
		}, g.circuitBreakers.RouteIDs, func() any { return g.circuitBreakers.Snapshots() }),
		newFeature("cache", "/cache", func(id string, rc config.RouteConfig) error {
			if rc.Cache.Enabled {
				g.caches.AddRoute(id, rc.Cache)
			}
			return nil
		}, g.caches.RouteIDs, func() any { return g.caches.Stats() }),
		newFeature("compression", "/compression", func(id string, rc config.RouteConfig) error {
			if rc.Compression.Enabled {
				g.compressors.AddRoute(id, rc.Compression)
			}
			return nil
		}, g.compressors.RouteIDs, func() any { return g.compressors.Stats() }),
		newFeature("validation", "", func(id string, rc config.RouteConfig) error {
			if rc.Validation.Enabled {
				return g.validators.AddRoute(id, rc.Validation)
			}
			return nil
		}, g.validators.RouteIDs, nil),
		newFeature("mirror", "/mirrors", func(id string, rc config.RouteConfig) error {
			if rc.Mirror.Enabled {
				return g.mirrors.AddRoute(id, rc.Mirror)
			}
			return nil
		}, g.mirrors.RouteIDs, func() any { return g.mirrors.Stats() }),
		newFeature("rules", "", func(id string, rc config.RouteConfig) error {
			if len(rc.Rules.Request) > 0 || len(rc.Rules.Response) > 0 {
				return g.routeRules.AddRoute(id, rc.Rules)
			}
			return nil
		}, g.routeRules.RouteIDs, func() any { return g.routeRules.Stats() }),
		newFeature("waf", "/waf", func(id string, rc config.RouteConfig) error {
			if rc.WAF.Enabled {
				return g.wafHandlers.AddRoute(id, rc.WAF)
			}
			return nil
		}, g.wafHandlers.RouteIDs, func() any { return g.wafHandlers.Stats() }),
		newFeature("graphql", "/graphql", func(id string, rc config.RouteConfig) error {
			if rc.GraphQL.Enabled {
				return g.graphqlParsers.AddRoute(id, rc.GraphQL)
			}
			return nil
		}, g.graphqlParsers.RouteIDs, func() any { return g.graphqlParsers.Stats() }),
		newFeature("coalesce", "/coalesce", func(id string, rc config.RouteConfig) error {
			if rc.Coalesce.Enabled {
				g.coalescers.AddRoute(id, rc.Coalesce)
			}
			return nil
		}, g.coalescers.RouteIDs, func() any { return g.coalescers.Stats() }),
		newFeature("ext_auth", "/ext-auth", func(id string, rc config.RouteConfig) error {
			if rc.ExtAuth.Enabled {
				return g.extAuths.AddRoute(id, rc.ExtAuth)
			}
			return nil
		}, g.extAuths.RouteIDs, func() any { return g.extAuths.Stats() }),
		newFeature("versioning", "/versioning", func(id string, rc config.RouteConfig) error {
			if rc.Versioning.Enabled {
				return g.versioners.AddRoute(id, rc.Versioning)
			}
			return nil
		}, g.versioners.RouteIDs, func() any { return g.versioners.Stats() }),
		newFeature("access_log", "/access-log", func(id string, rc config.RouteConfig) error {
			al := rc.AccessLog
			if al.Enabled != nil || al.Format != "" ||
				len(al.HeadersInclude) > 0 || len(al.HeadersExclude) > 0 ||
				al.Body.Enabled ||
				al.Conditions.SampleRate > 0 || len(al.Conditions.StatusCodes) > 0 ||
				len(al.Conditions.Methods) > 0 {
				return g.accessLogConfigs.AddRoute(id, al)
			}
			return nil
		}, g.accessLogConfigs.RouteIDs, func() any { return g.accessLogConfigs.Stats() }),
		newFeature("openapi", "/openapi", func(id string, rc config.RouteConfig) error {
			if rc.OpenAPI.SpecFile != "" || rc.OpenAPI.SpecID != "" {
				return g.openapiValidators.AddRoute(id, rc.OpenAPI)
			}
			return nil
		}, g.openapiValidators.RouteIDs, func() any { return g.openapiValidators.Stats() }),
		newFeature("timeout", "/timeouts", func(id string, rc config.RouteConfig) error {
			if rc.TimeoutPolicy.IsActive() {
				g.timeoutConfigs.AddRoute(id, rc.TimeoutPolicy)
			}
			return nil
		}, g.timeoutConfigs.RouteIDs, func() any { return g.timeoutConfigs.Stats() }),
		newFeature("proxy_rate_limit", "/proxy-rate-limits", func(id string, rc config.RouteConfig) error {
			if rc.ProxyRateLimit.Enabled {
				g.proxyRateLimiters.AddRoute(id, rc.ProxyRateLimit)
			}
			return nil
		}, g.proxyRateLimiters.RouteIDs, func() any { return g.proxyRateLimiters.Stats() }),
		newFeature("claims_propagation", "/claims-propagation", func(id string, rc config.RouteConfig) error {
			if rc.ClaimsPropagation.Enabled {
				g.claimsPropagators.AddRoute(id, rc.ClaimsPropagation)
			}
			return nil
		}, g.claimsPropagators.RouteIDs, func() any { return g.claimsPropagators.Stats() }),
		newFeature("token_exchange", "/token-exchange", func(id string, rc config.RouteConfig) error {
			if rc.TokenExchange.Enabled {
				return g.tokenExchangers.AddRoute(id, rc.TokenExchange)
			}
			return nil
		}, g.tokenExchangers.RouteIDs, func() any { return g.tokenExchangers.Stats() }),
		newFeature("mock_response", "/mock-responses", func(id string, rc config.RouteConfig) error {
			if rc.MockResponse.Enabled {
				g.mockHandlers.AddRoute(id, rc.MockResponse)
			}
			return nil
		}, g.mockHandlers.RouteIDs, func() any { return g.mockHandlers.Stats() }),
		newFeature("backend_auth", "/backend-auth", func(id string, rc config.RouteConfig) error {
			if rc.BackendAuth.Enabled {
				return g.backendAuths.AddRoute(id, rc.BackendAuth)
			}
			return nil
		}, g.backendAuths.RouteIDs, func() any { return g.backendAuths.Stats() }),
		newFeature("status_mapping", "/status-mapping", func(id string, rc config.RouteConfig) error {
			if rc.StatusMapping.Enabled && len(rc.StatusMapping.Mappings) > 0 {
				g.statusMappers.AddRoute(id, rc.StatusMapping.Mappings)
			}
			return nil
		}, g.statusMappers.RouteIDs, func() any { return g.statusMappers.Stats() }),
		newFeature("static_files", "/static-files", func(id string, rc config.RouteConfig) error {
			if rc.Static.Enabled {
				return g.staticFiles.AddRoute(id, rc.Static.Root, rc.Static.Index, rc.Static.Browse, rc.Static.CacheControl)
			}
			return nil
		}, g.staticFiles.RouteIDs, func() any { return g.staticFiles.Stats() }),
		newFeature("fastcgi", "/fastcgi", func(id string, rc config.RouteConfig) error {
			if rc.FastCGI.Enabled {
				return g.fastcgiHandlers.AddRoute(id, rc.FastCGI)
			}
			return nil
		}, g.fastcgiHandlers.RouteIDs, func() any { return g.fastcgiHandlers.Stats() }),
		newFeature("content_replacer", "/content-replacer", func(id string, rc config.RouteConfig) error {
			if rc.ContentReplacer.Enabled && len(rc.ContentReplacer.Replacements) > 0 {
				return g.contentReplacers.AddRoute(id, rc.ContentReplacer)
			}
			return nil
		}, g.contentReplacers.RouteIDs, func() any { return g.contentReplacers.Stats() }),
		newFeature("body_generator", "/body-generator", func(id string, rc config.RouteConfig) error {
			if rc.BodyGenerator.Enabled {
				return g.bodyGenerators.AddRoute(id, rc.BodyGenerator)
			}
			return nil
		}, g.bodyGenerators.RouteIDs, func() any { return g.bodyGenerators.Stats() }),
		newFeature("response_body_generator", "/response-body-generator", func(id string, rc config.RouteConfig) error {
			if rc.ResponseBodyGenerator.Enabled {
				return g.respBodyGenerators.AddRoute(id, rc.ResponseBodyGenerator)
			}
			return nil
		}, g.respBodyGenerators.RouteIDs, func() any { return g.respBodyGenerators.Stats() }),
		newFeature("param_forwarding", "/param-forwarding", func(id string, rc config.RouteConfig) error {
			if rc.ParamForwarding.Enabled {
				g.paramForwarders.AddRoute(id, rc.ParamForwarding)
			}
			return nil
		}, g.paramForwarders.RouteIDs, func() any { return g.paramForwarders.Stats() }),
		newFeature("content_negotiation", "/content-negotiation", func(id string, rc config.RouteConfig) error {
			if rc.ContentNegotiation.Enabled || rc.OutputEncoding != "" {
				cnCfg := rc.ContentNegotiation
				if !cnCfg.Enabled && rc.OutputEncoding != "" {
					cnCfg.Enabled = true
					cnCfg.Supported = []string{"json", "xml", "yaml"}
					cnCfg.Default = "json"
				}
				return g.contentNegotiators.AddRoute(id, cnCfg, rc.OutputEncoding)
			}
			return nil
		}, g.contentNegotiators.RouteIDs, func() any { return g.contentNegotiators.Stats() }),
		newFeature("backend_encoding", "/backend-encoding", func(id string, rc config.RouteConfig) error {
			if rc.BackendEncoding.Encoding != "" {
				g.backendEncoders.AddRoute(id, rc.BackendEncoding)
			}
			return nil
		}, g.backendEncoders.RouteIDs, func() any { return g.backendEncoders.Stats() }),
		newFeature("sse", "/sse", func(id string, rc config.RouteConfig) error {
			if rc.SSE.Enabled {
				g.sseHandlers.AddRoute(id, rc.SSE)
			}
			return nil
		}, g.sseHandlers.RouteIDs, func() any { return g.sseHandlers.Stats() }),
		newFeature("pii_redaction", "/pii-redaction", func(id string, rc config.RouteConfig) error {
			if rc.PIIRedaction.Enabled {
				return g.piiRedactors.AddRoute(id, rc.PIIRedaction)
			}
			return nil
		}, g.piiRedactors.RouteIDs, func() any { return g.piiRedactors.Stats() }),
		newFeature("field_encryption", "/field-encryption", func(id string, rc config.RouteConfig) error {
			if rc.FieldEncryption.Enabled {
				return g.fieldEncryptors.AddRoute(id, rc.FieldEncryption)
			}
			return nil
		}, g.fieldEncryptors.RouteIDs, func() any { return g.fieldEncryptors.Stats() }),

		// Merge features: merge per-route with global config
		newFeature("throttle", "", func(id string, rc config.RouteConfig) error {
			tc := rc.TrafficShaping.Throttle
			if tc.Enabled {
				merged := trafficshape.MergeThrottleConfig(tc, cfg.TrafficShaping.Throttle)
				g.throttlers.AddRoute(id, merged)
			} else if cfg.TrafficShaping.Throttle.Enabled {
				g.throttlers.AddRoute(id, cfg.TrafficShaping.Throttle)
			}
			return nil
		}, g.throttlers.RouteIDs, func() any { return g.throttlers.Stats() }),
		newFeature("bandwidth", "", func(id string, rc config.RouteConfig) error {
			bc := rc.TrafficShaping.Bandwidth
			if bc.Enabled {
				merged := trafficshape.MergeBandwidthConfig(bc, cfg.TrafficShaping.Bandwidth)
				g.bandwidthLimiters.AddRoute(id, merged)
			} else if cfg.TrafficShaping.Bandwidth.Enabled {
				g.bandwidthLimiters.AddRoute(id, cfg.TrafficShaping.Bandwidth)
			}
			return nil
		}, g.bandwidthLimiters.RouteIDs, func() any { return g.bandwidthLimiters.Stats() }),
		newFeature("priority", "", func(id string, rc config.RouteConfig) error {
			pc := rc.TrafficShaping.Priority
			if pc.Enabled {
				merged := trafficshape.MergePriorityConfig(pc, cfg.TrafficShaping.Priority)
				g.priorityConfigs.AddRoute(id, merged)
			} else if cfg.TrafficShaping.Priority.Enabled {
				g.priorityConfigs.AddRoute(id, cfg.TrafficShaping.Priority)
			}
			return nil
		}, g.priorityConfigs.RouteIDs, nil),
		newFeature("fault_injection", "", func(id string, rc config.RouteConfig) error {
			fi := rc.TrafficShaping.FaultInjection
			if fi.Enabled {
				merged := trafficshape.MergeFaultInjectionConfig(fi, cfg.TrafficShaping.FaultInjection)
				g.faultInjectors.AddRoute(id, merged)
			} else if cfg.TrafficShaping.FaultInjection.Enabled {
				g.faultInjectors.AddRoute(id, cfg.TrafficShaping.FaultInjection)
			}
			return nil
		}, g.faultInjectors.RouteIDs, func() any { return g.faultInjectors.Stats() }),
		newFeature("adaptive_concurrency", "/adaptive-concurrency", func(id string, rc config.RouteConfig) error {
			ac := rc.TrafficShaping.AdaptiveConcurrency
			if ac.Enabled {
				merged := trafficshape.MergeAdaptiveConcurrencyConfig(ac, cfg.TrafficShaping.AdaptiveConcurrency)
				g.adaptiveLimiters.AddRoute(id, merged)
			} else if cfg.TrafficShaping.AdaptiveConcurrency.Enabled {
				g.adaptiveLimiters.AddRoute(id, cfg.TrafficShaping.AdaptiveConcurrency)
			}
			return nil
		}, g.adaptiveLimiters.RouteIDs, func() any { return g.adaptiveLimiters.Stats() }),
		newFeature("request_queue", "/request-queues", func(id string, rc config.RouteConfig) error {
			rq := rc.TrafficShaping.RequestQueue
			if rq.Enabled {
				merged := requestqueue.MergeRequestQueueConfig(rq, cfg.TrafficShaping.RequestQueue)
				g.requestQueues.AddRoute(id, merged)
			} else if cfg.TrafficShaping.RequestQueue.Enabled {
				g.requestQueues.AddRoute(id, cfg.TrafficShaping.RequestQueue)
			}
			return nil
		}, g.requestQueues.RouteIDs, func() any { return g.requestQueues.Stats() }),
		newFeature("error_pages", "/error-pages", func(id string, rc config.RouteConfig) error {
			if cfg.ErrorPages.IsActive() || rc.ErrorPages.IsActive() {
				return g.errorPages.AddRoute(id, cfg.ErrorPages, rc.ErrorPages)
			}
			return nil
		}, g.errorPages.RouteIDs, func() any { return g.errorPages.Stats() }),
		newFeature("nonce", "/nonces", func(id string, rc config.RouteConfig) error {
			if rc.Nonce.Enabled {
				merged := nonce.MergeNonceConfig(rc.Nonce, cfg.Nonce)
				return g.nonceCheckers.AddRoute(id, merged, g.redisClient)
			}
			if cfg.Nonce.Enabled {
				return g.nonceCheckers.AddRoute(id, cfg.Nonce, g.redisClient)
			}
			return nil
		}, g.nonceCheckers.RouteIDs, func() any { return g.nonceCheckers.Stats() }),
		newFeature("csrf", "/csrf", func(id string, rc config.RouteConfig) error {
			if rc.CSRF.Enabled {
				merged := csrf.MergeCSRFConfig(rc.CSRF, cfg.CSRF)
				return g.csrfProtectors.AddRoute(id, merged)
			}
			if cfg.CSRF.Enabled {
				return g.csrfProtectors.AddRoute(id, cfg.CSRF)
			}
			return nil
		}, g.csrfProtectors.RouteIDs, func() any { return g.csrfProtectors.Stats() }),
		newFeature("idempotency", "/idempotency", func(id string, rc config.RouteConfig) error {
			if rc.Idempotency.Enabled {
				merged := idempotency.MergeIdempotencyConfig(rc.Idempotency, cfg.Idempotency)
				return g.idempotencyHandlers.AddRoute(id, merged, g.redisClient)
			}
			if cfg.Idempotency.Enabled {
				return g.idempotencyHandlers.AddRoute(id, cfg.Idempotency, g.redisClient)
			}
			return nil
		}, g.idempotencyHandlers.RouteIDs, func() any { return g.idempotencyHandlers.Stats() }),
		newFeature("request_dedup", "/request-dedup", func(id string, rc config.RouteConfig) error {
			if rc.RequestDedup.Enabled {
				return g.dedupHandlers.AddRoute(id, rc.RequestDedup, g.redisClient)
			}
			return nil
		}, g.dedupHandlers.RouteIDs, func() any { return g.dedupHandlers.Stats() }),
		newFeature("ip_blocklist", "/ip-blocklist", func(id string, rc config.RouteConfig) error {
			if rc.IPBlocklist.Enabled {
				merged := ipblocklist.MergeIPBlocklistConfig(rc.IPBlocklist, cfg.IPBlocklist)
				return g.ipBlocklists.AddRoute(id, merged)
			}
			if cfg.IPBlocklist.Enabled {
				return g.ipBlocklists.AddRoute(id, cfg.IPBlocklist)
			}
			return nil
		}, g.ipBlocklists.RouteIDs, func() any { return g.ipBlocklists.Stats() }),
		newFeature("client_mtls", "/client-mtls", func(id string, rc config.RouteConfig) error {
			if rc.ClientMTLS.Enabled {
				merged := clientmtls.MergeClientMTLSConfig(rc.ClientMTLS, cfg.ClientMTLS)
				return g.clientMTLSVerifiers.AddRoute(id, merged)
			}
			if cfg.ClientMTLS.Enabled {
				return g.clientMTLSVerifiers.AddRoute(id, cfg.ClientMTLS)
			}
			return nil
		}, g.clientMTLSVerifiers.RouteIDs, func() any { return g.clientMTLSVerifiers.Stats() }),
		newFeature("geo", "/geo", func(id string, rc config.RouteConfig) error {
			if g.geoProvider == nil {
				return nil
			}
			if rc.Geo.Enabled {
				merged := geo.MergeGeoConfig(rc.Geo, cfg.Geo)
				return g.geoFilters.AddRoute(id, merged, g.geoProvider)
			}
			if cfg.Geo.Enabled {
				return g.geoFilters.AddRoute(id, cfg.Geo, g.geoProvider)
			}
			return nil
		}, g.geoFilters.RouteIDs, func() any { return g.geoFilters.Stats() }),
		newFeature("backend_signing", "/signing", func(id string, rc config.RouteConfig) error {
			if rc.BackendSigning.Enabled {
				merged := signing.MergeSigningConfig(rc.BackendSigning, cfg.BackendSigning)
				return g.backendSigners.AddRoute(id, merged)
			}
			if cfg.BackendSigning.Enabled {
				return g.backendSigners.AddRoute(id, cfg.BackendSigning)
			}
			return nil
		}, g.backendSigners.RouteIDs, func() any { return g.backendSigners.Stats() }),
		newFeature("request_decompression", "/decompression", func(id string, rc config.RouteConfig) error {
			if rc.RequestDecompression.Enabled {
				merged := decompress.MergeDecompressionConfig(rc.RequestDecompression, cfg.RequestDecompression)
				g.decompressors.AddRoute(id, merged)
			} else if cfg.RequestDecompression.Enabled {
				g.decompressors.AddRoute(id, cfg.RequestDecompression)
			}
			return nil
		}, g.decompressors.RouteIDs, func() any { return g.decompressors.Stats() }),
		newFeature("response_limit", "/response-limits", func(id string, rc config.RouteConfig) error {
			if rc.ResponseLimit.Enabled {
				merged := responselimit.MergeResponseLimitConfig(rc.ResponseLimit, cfg.ResponseLimit)
				g.responseLimiters.AddRoute(id, merged)
			} else if cfg.ResponseLimit.Enabled {
				g.responseLimiters.AddRoute(id, cfg.ResponseLimit)
			}
			return nil
		}, g.responseLimiters.RouteIDs, func() any { return g.responseLimiters.Stats() }),
		newFeature("security_headers", "/security-headers", func(id string, rc config.RouteConfig) error {
			if rc.SecurityHeaders.Enabled {
				merged := securityheaders.MergeSecurityHeadersConfig(rc.SecurityHeaders, cfg.SecurityHeaders)
				g.securityHeaders.AddRoute(id, merged)
			} else if cfg.SecurityHeaders.Enabled {
				g.securityHeaders.AddRoute(id, cfg.SecurityHeaders)
			}
			return nil
		}, g.securityHeaders.RouteIDs, func() any { return g.securityHeaders.Stats() }),
		newFeature("maintenance", "/maintenance", func(id string, rc config.RouteConfig) error {
			if rc.Maintenance.Enabled {
				merged := maintenance.MergeMaintenanceConfig(rc.Maintenance, cfg.Maintenance)
				g.maintenanceHandlers.AddRoute(id, merged)
			} else if cfg.Maintenance.Enabled {
				g.maintenanceHandlers.AddRoute(id, cfg.Maintenance)
			}
			return nil
		}, g.maintenanceHandlers.RouteIDs, func() any { return g.maintenanceHandlers.Stats() }),
		newFeature("bot_detection", "/bot-detection", func(id string, rc config.RouteConfig) error {
			if rc.BotDetection.Enabled {
				merged := botdetect.MergeBotDetectionConfig(rc.BotDetection, cfg.BotDetection)
				return g.botDetectors.AddRoute(id, merged)
			}
			if cfg.BotDetection.Enabled {
				return g.botDetectors.AddRoute(id, cfg.BotDetection)
			}
			return nil
		}, g.botDetectors.RouteIDs, func() any { return g.botDetectors.Stats() }),
		newFeature("spike_arrest", "/spike-arrest", func(id string, rc config.RouteConfig) error {
			rc2 := rc.SpikeArrest
			if !rc2.Enabled && !cfg.SpikeArrest.Enabled {
				return nil
			}
			merged := spikearrest.MergeSpikeArrestConfig(rc2, cfg.SpikeArrest)
			g.spikeArresters.AddRoute(id, merged)
			return nil
		}, g.spikeArresters.RouteIDs, func() any { return g.spikeArresters.Stats() }),
		newFeature("inbound_signing", "/inbound-signing", func(id string, rc config.RouteConfig) error {
			if rc.InboundSigning.Enabled {
				merged := inboundsigning.MergeInboundSigningConfig(rc.InboundSigning, cfg.InboundSigning)
				return g.inboundVerifiers.AddRoute(id, merged)
			}
			if cfg.InboundSigning.Enabled {
				return g.inboundVerifiers.AddRoute(id, cfg.InboundSigning)
			}
			return nil
		}, g.inboundVerifiers.RouteIDs, func() any { return g.inboundVerifiers.Stats() }),
		newFeature("cdn_cache_headers", "/cdn-cache-headers", func(id string, rc config.RouteConfig) error {
			merged := cdnheaders.MergeCDNCacheConfig(rc.CDNCacheHeaders, cfg.CDNCacheHeaders)
			if merged.Enabled {
				g.cdnHeaders.AddRoute(id, merged)
			}
			return nil
		}, g.cdnHeaders.RouteIDs, func() any { return g.cdnHeaders.Stats() }),
		newFeature("quota", "/quotas", func(id string, rc config.RouteConfig) error {
			if rc.Quota.Enabled {
				g.quotaEnforcers.AddRoute(id, rc.Quota, g.redisClient)
			}
			return nil
		}, g.quotaEnforcers.RouteIDs, func() any { return g.quotaEnforcers.Stats() }),
		newFeature("tenant", "/tenants", func(id string, rc config.RouteConfig) error {
			return nil // global manager, no per-route setup needed
		}, func() []string {
			if g.tenantManager != nil { return []string{"global"} }; return nil
		}, func() any {
			if g.tenantManager != nil { return g.tenantManager.Stats() }; return nil
		}),

		newFeature("baggage", "/baggage", func(id string, rc config.RouteConfig) error {
			if rc.Baggage.Enabled {
				return g.baggagePropagators.AddRoute(id, rc.Baggage)
			}
			return nil
		}, g.baggagePropagators.RouteIDs, func() any { return g.baggagePropagators.Stats() }),
		newFeature("audit_log", "/audit-log", func(id string, rc config.RouteConfig) error {
			if rc.AuditLog.Enabled {
				merged := auditlog.MergeAuditLogConfig(rc.AuditLog, cfg.AuditLog)
				return g.auditLoggers.AddRoute(id, merged)
			}
			if cfg.AuditLog.Enabled {
				return g.auditLoggers.AddRoute(id, cfg.AuditLog)
			}
			return nil
		}, g.auditLoggers.RouteIDs, func() any { return g.auditLoggers.Stats() }),

		newFeature("modifiers", "/modifiers", func(id string, rc config.RouteConfig) error {
			if len(rc.Modifiers) > 0 {
				return g.modifierChains.AddRoute(id, rc.Modifiers)
			}
			return nil
		}, g.modifierChains.RouteIDs, func() any { return g.modifierChains.Stats() }),
		newFeature("jmespath", "/jmespath", func(id string, rc config.RouteConfig) error {
			if rc.JMESPath.Enabled {
				return g.jmespathHandlers.AddRoute(id, rc.JMESPath)
			}
			return nil
		}, g.jmespathHandlers.RouteIDs, func() any { return g.jmespathHandlers.Stats() }),
		newFeature("field_replacer", "/field-replacer", func(id string, rc config.RouteConfig) error {
			if rc.FieldReplacer.Enabled {
				return g.fieldReplacers.AddRoute(id, rc.FieldReplacer)
			}
			return nil
		}, g.fieldReplacers.RouteIDs, func() any { return g.fieldReplacers.Stats() }),
		newFeature("error_handling", "/error-handling", func(id string, rc config.RouteConfig) error {
			if rc.ErrorHandling.Mode != "" && rc.ErrorHandling.Mode != "default" {
				g.errorHandlers.AddRoute(id, rc.ErrorHandling)
			}
			return nil
		}, g.errorHandlers.RouteIDs, func() any { return g.errorHandlers.Stats() }),
		newFeature("lua", "/lua", func(id string, rc config.RouteConfig) error {
			if rc.Lua.Enabled {
				return g.luaScripters.AddRoute(id, rc.Lua)
			}
			return nil
		}, g.luaScripters.RouteIDs, func() any { return g.luaScripters.Stats() }),
		newFeature("wasm", "/wasm-plugins", func(id string, rc config.RouteConfig) error {
			if len(rc.WasmPlugins) > 0 {
				return g.wasmPlugins.AddRoute(id, rc.WasmPlugins)
			}
			return nil
		}, g.wasmPlugins.RouteIDs, func() any { return g.wasmPlugins.Stats() }),

		// No-op features: setup handled elsewhere (need transport/balancer)
		noOpFeature("canary", "/canary", g.canaryControllers.RouteIDs, func() any { return g.canaryControllers.Stats() }),
		noOpFeature("outlier_detection", "/outlier-detection", g.outlierDetectors.RouteIDs, func() any { return g.outlierDetectors.Stats() }),
		noOpFeature("backpressure", "/backpressure", g.backpressureHandlers.RouteIDs, func() any { return g.backpressureHandlers.Stats() }),
		noOpFeature("blue_green", "/blue-green", g.blueGreenControllers.RouteIDs, func() any { return g.blueGreenControllers.Stats() }),
		noOpFeature("ab_test", "/ab-tests", g.abTests.RouteIDs, func() any { return g.abTests.Stats() }),
		noOpFeature("sequential", "/sequential", g.sequentialHandlers.RouteIDs, func() any { return g.sequentialHandlers.Stats() }),
		noOpFeature("aggregate", "/aggregate", g.aggregateHandlers.RouteIDs, func() any { return g.aggregateHandlers.Stats() }),
		noOpFeature("lambda", "/lambda", g.lambdaHandlers.RouteIDs, func() any { return g.lambdaHandlers.Stats() }),
		noOpFeature("amqp", "/amqp", g.amqpHandlers.RouteIDs, func() any { return g.amqpHandlers.Stats() }),
		noOpFeature("pubsub", "/pubsub", g.pubsubHandlers.RouteIDs, func() any { return g.pubsubHandlers.Stats() }),

		newFeature("traffic_replay", "/traffic-replay", func(id string, rc config.RouteConfig) error {
			if rc.TrafficReplay.Enabled {
				return g.trafficReplay.AddRoute(id, rc.TrafficReplay)
			}
			return nil
		}, g.trafficReplay.RouteIDs, func() any { return g.trafficReplay.Stats() }),

		noOpFeature("session_affinity", "/session-affinity", func() []string {
			var ids []string
			for routeID, rp := range *g.routeProxies.Load() {
				if _, ok := rp.GetBalancer().(*loadbalancer.SessionAffinityBalancer); ok {
					ids = append(ids, routeID)
				}
			}
			return ids
		}, func() any {
			result := make(map[string]interface{})
			for routeID, rp := range *g.routeProxies.Load() {
				if sa, ok := rp.GetBalancer().(*loadbalancer.SessionAffinityBalancer); ok {
					result[routeID] = map[string]interface{}{
						"cookie_name": sa.CookieName(),
						"ttl":         sa.TTL().String(),
					}
				}
			}
			if len(result) == 0 {
				return nil
			}
			return result
		}),

		// Non-per-route managers exposed as Features for auto admin + dashboard
		noOpFeature("retries", "/retries", func() []string { return nil }, func() any {
			result := make(map[string]interface{})
			for routeID, rp := range *g.routeProxies.Load() {
				if m := rp.GetRetryMetrics(); m != nil {
					result[routeID] = m.Snapshot()
				}
			}
			if len(result) == 0 {
				return nil
			}
			return result
		}),
		noOpFeature("retry_budget_pools", "/retry-budget-pools", func() []string { return nil }, func() any {
			if len(g.budgetPools) == 0 {
				return nil
			}
			result := make(map[string]interface{}, len(g.budgetPools))
			for name, b := range g.budgetPools {
				result[name] = b.Stats()
			}
			return result
		}),
		noOpFeature("traffic_splits", "/traffic-splits", func() []string { return nil }, func() any {
			return g.GetTrafficSplitStats()
		}),
		noOpFeature("trusted_proxies", "/trusted-proxies", func() []string { return nil }, func() any {
			if g.realIPExtractor == nil {
				return map[string]interface{}{"enabled": false}
			}
			return g.realIPExtractor.Stats()
		}),
		noOpFeature("tracing", "/tracing", func() []string { return nil }, func() any {
			if g.tracer == nil {
				return map[string]interface{}{"enabled": false}
			}
			return g.tracer.Status()
		}),
		noOpFeature("service_rate_limit", "/service-rate-limit", func() []string { return nil }, func() any {
			if g.serviceLimiter == nil {
				return map[string]interface{}{"enabled": false}
			}
			return g.serviceLimiter.Stats()
		}),
		noOpFeature("webhooks", "/webhooks", func() []string { return nil }, func() any {
			if g.webhookDispatcher == nil {
				return map[string]interface{}{"enabled": false}
			}
			return g.webhookDispatcher.Stats()
		}),
		noOpFeature("follow_redirects", "/follow-redirects", func() []string { return nil }, func() any {
			return g.GetFollowRedirectStats()
		}),
		noOpFeature("protocol_translators", "/protocol-translators",
			g.translators.RouteIDs, func() any { return g.translators.Stats() }),
		noOpFeature("grpc_proxy", "/grpc-proxy",
			g.grpcHandlers.RouteIDs, func() any { return g.grpcHandlers.Stats() }),
	}

	// Initialize global IP filter
	if cfg.IPFilter.Enabled {
		var err error
		g.globalIPFilter, err = ipfilter.New(cfg.IPFilter)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize global IP filter: %w", err)
		}
	}

	// Initialize geo provider and global geo filter
	if cfg.Geo.Enabled && cfg.Geo.Database != "" {
		var err error
		g.geoProvider, err = geo.NewProvider(cfg.Geo.Database)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize geo provider: %w", err)
		}
		g.globalGeo, err = geo.New("_global", cfg.Geo, g.geoProvider)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize global geo filter: %w", err)
		}
	}

	// Initialize trusted proxies / real IP extractor
	if len(cfg.TrustedProxies.CIDRs) > 0 {
		var err error
		g.realIPExtractor, err = realip.New(cfg.TrustedProxies.CIDRs, cfg.TrustedProxies.Headers, cfg.TrustedProxies.MaxHops)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize trusted proxies: %w", err)
		}
	}

	// Initialize global rules engine
	if len(cfg.Rules.Request) > 0 || len(cfg.Rules.Response) > 0 {
		var err error
		g.globalRules, err = rules.NewEngine(cfg.Rules.Request, cfg.Rules.Response)
		if err != nil {
			return nil, fmt.Errorf("failed to compile global rules: %w", err)
		}
	}

	// Initialize tracer
	if cfg.Tracing.Enabled {
		var err error
		g.tracer, err = tracing.New(cfg.Tracing)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize tracer: %w", err)
		}
	}

	// Initialize Redis client if configured
	if cfg.Redis.Address != "" {
		g.redisClient = redis.NewClient(&redis.Options{
			Addr:        cfg.Redis.Address,
			Password:    cfg.Redis.Password,
			DB:          cfg.Redis.DB,
			PoolSize:    cfg.Redis.PoolSize,
			DialTimeout: cfg.Redis.DialTimeout,
		})
		g.caches.SetRedisClient(g.redisClient)
	}

	// Initialize tenant manager
	if cfg.Tenants.Enabled {
		g.tenantManager = tenant.NewManager(cfg.Tenants, g.redisClient)
	}

	// Initialize HTTPS redirect
	if cfg.HTTPSRedirect.Enabled {
		g.httpsRedirect = httpsredirect.New(cfg.HTTPSRedirect)
	}

	// Initialize allowed hosts
	if cfg.AllowedHosts.Enabled {
		g.allowedHosts = allowedhosts.New(cfg.AllowedHosts)
	}

	// Initialize token revocation checker
	if cfg.TokenRevocation.Enabled {
		g.tokenChecker = tokenrevoke.New(cfg.TokenRevocation, g.redisClient)
	}

	// Initialize service-level rate limiter
	if cfg.ServiceRateLimit.Enabled {
		g.serviceLimiter = serviceratelimit.New(cfg.ServiceRateLimit)
	}

	// Initialize debug endpoint
	if cfg.DebugEndpoint.Enabled {
		g.debugHandler = debug.New(cfg.DebugEndpoint, cfg)
	}

	// Initialize SSRF protection dialer (for admin stats)
	if cfg.SSRFProtection.Enabled {
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		if sd, err := ssrf.New(dialer, cfg.SSRFProtection); err == nil {
			g.ssrfDialer = sd
		}
	}

	// Initialize global IP blocklist
	if cfg.IPBlocklist.Enabled {
		var err error
		g.globalBlocklist, err = ipblocklist.New(cfg.IPBlocklist)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize global IP blocklist: %w", err)
		}
	}

	// Detect HTTP/3 port for Alt-Svc header
	for _, lc := range cfg.Listeners {
		if lc.Protocol == config.ProtocolHTTP && lc.HTTP.EnableHTTP3 {
			_, port, err := net.SplitHostPort(lc.Address)
			if err == nil {
				g.http3AltSvcPort = port
			}
			break
		}
	}

	// Initialize webhook dispatcher if enabled
	if cfg.Webhooks.Enabled {
		g.webhookDispatcher = webhook.NewDispatcher(cfg.Webhooks)

		// Wire circuit breaker state change callback
		g.circuitBreakers.SetOnStateChange(func(routeID, from, to string) {
			g.webhookDispatcher.Emit(webhook.NewEvent(webhook.CircuitBreakerStateChange, routeID, map[string]interface{}{
				"from": from, "to": to,
			}))
		})

		// Wire canary event callback
		g.canaryControllers.SetOnEvent(func(routeID, eventType string, data map[string]interface{}) {
			g.webhookDispatcher.Emit(webhook.NewEvent(webhook.EventType(eventType), routeID, data))
		})

		// Wire outlier detection callbacks
		g.outlierDetectors.SetCallbacks(
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

	// Initialize health checker
	g.healthChecker = health.NewChecker(health.Config{
		OnChange: func(url string, status health.Status) {
			logging.Info("Backend health changed",
				zap.String("backend", url),
				zap.String("status", string(status)),
			)
			g.updateBackendHealth(url, status)

			// Emit webhook event for backend health changes
			if g.webhookDispatcher != nil {
				var eventType webhook.EventType
				if status == health.StatusHealthy {
					eventType = webhook.BackendHealthy
				} else {
					eventType = webhook.BackendUnhealthy
				}
				g.webhookDispatcher.Emit(webhook.NewEvent(eventType, "", map[string]interface{}{
					"url":    url,
					"status": string(status),
				}))
			}
		},
	})

	// Initialize proxy with transport pool
	pool := g.buildTransportPool(cfg)
	g.proxy = proxy.New(proxy.Config{
		TransportPool: pool,
		HealthChecker: g.healthChecker,
	})

	// Initialize registry
	if err := g.initRegistry(); err != nil {
		return nil, fmt.Errorf("failed to initialize registry: %w", err)
	}

	// Initialize authentication
	if err := g.initAuth(); err != nil {
		return nil, fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Initialize routes
	if err := g.initRoutes(); err != nil {
		return nil, fmt.Errorf("failed to initialize routes: %w", err)
	}

	return g, nil
}

// initRegistry initializes the service registry
func (g *Gateway) initRegistry() error {
	var err error

	switch g.config.Registry.Type {
	case "consul":
		g.registry, err = consul.New(g.config.Registry.Consul)
	case "etcd":
		g.registry, err = etcd.New(g.config.Registry.Etcd)
	case "memory":
		if g.config.Registry.Memory.APIEnabled {
			g.registry, err = memory.NewWithAPI(g.config.Registry.Memory.APIPort)
		} else {
			g.registry = memory.New()
		}
	case "dns":
		g.registry, err = dnsregistry.New(g.config.Registry.DNSSRV)
	default:
		g.registry = memory.New()
	}

	return err
}

// initAuth initializes authentication providers
func (g *Gateway) initAuth() error {
	// Initialize API Key auth
	if g.config.Authentication.APIKey.Enabled {
		g.apiKeyAuth = auth.NewAPIKeyAuth(g.config.Authentication.APIKey)

		// Initialize API Key Manager if management is enabled
		if g.config.Authentication.APIKey.Management.Enabled {
			mgmt := g.config.Authentication.APIKey.Management
			var store auth.KeyStore
			store = auth.NewMemoryKeyStore(60 * time.Second)

			var defaultRL *auth.KeyRateLimit
			if mgmt.DefaultRateLimit != nil {
				defaultRL = &auth.KeyRateLimit{
					Rate:   mgmt.DefaultRateLimit.Rate,
					Period: mgmt.DefaultRateLimit.Period,
					Burst:  mgmt.DefaultRateLimit.Burst,
				}
			}

			manager := auth.NewAPIKeyManager(auth.KeyManagerConfig{
				KeyLength: mgmt.KeyLength,
				KeyPrefix: mgmt.KeyPrefix,
				DefaultRL: defaultRL,
				Store:     store,
			})
			g.apiKeyAuth.SetManager(manager)
		}
	}

	// Initialize JWT auth
	if g.config.Authentication.JWT.Enabled {
		var err error
		g.jwtAuth, err = auth.NewJWTAuth(g.config.Authentication.JWT)
		if err != nil {
			return err
		}
	}

	// Initialize OAuth auth
	if g.config.Authentication.OAuth.Enabled {
		var err error
		g.oauthAuth, err = auth.NewOAuthAuth(g.config.Authentication.OAuth)
		if err != nil {
			return err
		}
	}

	return nil
}

// initRoutes initializes all routes from configuration
func (g *Gateway) initRoutes() error {
	for _, routeCfg := range g.config.Routes {
		if err := g.addRoute(routeCfg); err != nil {
			return fmt.Errorf("failed to add route %s: %w", routeCfg.ID, err)
		}
	}
	return nil
}

// resolveUpstreamRefs resolves upstream references in a route config by populating
// inline backends from named upstreams. The returned config is a copy with all
// upstream refs resolved to their backend lists. LB settings are also inherited
// from the upstream when the route doesn't specify them.
func resolveUpstreamRefs(cfg *config.Config, routeCfg config.RouteConfig) config.RouteConfig {
	if cfg.Upstreams == nil {
		return routeCfg
	}

	// Resolve route-level upstream
	if routeCfg.Upstream != "" {
		if us, ok := cfg.Upstreams[routeCfg.Upstream]; ok {
			routeCfg.Backends = us.Backends
			routeCfg.Service = us.Service
			if routeCfg.LoadBalancer == "" {
				routeCfg.LoadBalancer = us.LoadBalancer
			}
			if routeCfg.ConsistentHash == (config.ConsistentHashConfig{}) {
				routeCfg.ConsistentHash = us.ConsistentHash
			}
		}
	}

	// Resolve traffic split upstream refs
	for i, split := range routeCfg.TrafficSplit {
		if split.Upstream != "" {
			if us, ok := cfg.Upstreams[split.Upstream]; ok {
				routeCfg.TrafficSplit[i].Backends = us.Backends
			}
		}
	}

	// Resolve versioning upstream refs
	if routeCfg.Versioning.Enabled {
		for ver, vcfg := range routeCfg.Versioning.Versions {
			if vcfg.Upstream != "" {
				if us, ok := cfg.Upstreams[vcfg.Upstream]; ok {
					vcfg.Backends = us.Backends
					routeCfg.Versioning.Versions[ver] = vcfg
				}
			}
		}
	}

	// Resolve mirror upstream ref
	if routeCfg.Mirror.Enabled && routeCfg.Mirror.Upstream != "" {
		if us, ok := cfg.Upstreams[routeCfg.Mirror.Upstream]; ok {
			routeCfg.Mirror.Backends = us.Backends
		}
	}

	return routeCfg
}

// upstreamHealthCheck returns the upstream-level health check config for a backend URL,
// merging global → upstream → per-backend configs.
func upstreamHealthCheck(backendURL string, global config.HealthCheckConfig, upstream *config.HealthCheckConfig, perBackend *config.HealthCheckConfig) health.Backend {
	// Start from global, then apply upstream-level overrides, then per-backend
	merged := global
	if upstream != nil {
		if upstream.Path != "" {
			merged.Path = upstream.Path
		}
		if upstream.Method != "" {
			merged.Method = upstream.Method
		}
		if upstream.Interval > 0 {
			merged.Interval = upstream.Interval
		}
		if upstream.Timeout > 0 {
			merged.Timeout = upstream.Timeout
		}
		if upstream.HealthyAfter > 0 {
			merged.HealthyAfter = upstream.HealthyAfter
		}
		if upstream.UnhealthyAfter > 0 {
			merged.UnhealthyAfter = upstream.UnhealthyAfter
		}
		if len(upstream.ExpectedStatus) > 0 {
			merged.ExpectedStatus = upstream.ExpectedStatus
		}
	}
	return mergeHealthCheckConfig(backendURL, merged, perBackend)
}

// addRoute adds a single route
func (g *Gateway) addRoute(routeCfg config.RouteConfig) error {
	// Resolve upstream references into inline backends/service/LB settings
	routeCfg = resolveUpstreamRefs(g.config, routeCfg)

	// Add route to router
	if err := g.router.AddRoute(routeCfg); err != nil {
		return err
	}

	route := g.router.GetRoute(routeCfg.ID)
	if route == nil {
		return fmt.Errorf("route not found after adding: %s", routeCfg.ID)
	}

	// Set upstream name on route for transport pool resolution
	route.UpstreamName = routeCfg.Upstream

	// Set up backends (skip for echo, sequential, and aggregate routes — no backend needed)
	var routeProxy *proxy.RouteProxy
	if !routeCfg.Echo && !routeCfg.Sequential.Enabled && !routeCfg.Aggregate.Enabled {
		var backends []*loadbalancer.Backend

		// Check if using service discovery
		if routeCfg.Service.Name != "" {
			ctx := context.Background()

			// Discover initial backends
			services, err := g.registry.DiscoverWithTags(ctx, routeCfg.Service.Name, routeCfg.Service.Tags)
			if err != nil {
				logging.Warn("Failed to discover service",
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

			// Start watching for changes
			g.watchService(routeCfg.ID, routeCfg.Service.Name, routeCfg.Service.Tags)
		} else {
			// Use static backends
			var usHC *config.HealthCheckConfig
			if routeCfg.Upstream != "" {
				if us, ok := g.config.Upstreams[routeCfg.Upstream]; ok {
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

				// Add to health checker (upstream health check sits between global and per-backend)
				g.healthChecker.AddBackend(upstreamHealthCheck(b.URL, g.config.HealthCheck, usHC, b.HealthCheck))
			}
		}

		// Create route proxy with the appropriate balancer
		if routeCfg.Versioning.Enabled {
			versionBackends := make(map[string][]*loadbalancer.Backend)
			for ver, vcfg := range routeCfg.Versioning.Versions {
				var vBacks []*loadbalancer.Backend
				var verUSHC *config.HealthCheckConfig
				if vcfg.Upstream != "" {
					if us, ok := g.config.Upstreams[vcfg.Upstream]; ok {
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
					g.healthChecker.AddBackend(upstreamHealthCheck(b.URL, g.config.HealthCheck, verUSHC, b.HealthCheck))
				}
				versionBackends[ver] = vBacks
			}
			vb := loadbalancer.NewVersionedBalancer(versionBackends, routeCfg.Versioning.DefaultVersion)
			routeProxy = proxy.NewRouteProxyWithBalancer(g.proxy, route, vb)
		} else if len(routeCfg.TrafficSplit) > 0 {
			var wb *loadbalancer.WeightedBalancer
			if routeCfg.Sticky.Enabled {
				wb = loadbalancer.NewWeightedBalancerWithSticky(routeCfg.TrafficSplit, routeCfg.Sticky)
			} else {
				wb = loadbalancer.NewWeightedBalancer(routeCfg.TrafficSplit)
			}
			routeProxy = proxy.NewRouteProxyWithBalancer(g.proxy, route, wb)
		} else {
			bal := createBalancer(routeCfg, backends)
			// Wrap with tenant-aware balancer if per-tenant backends configured
			if len(routeCfg.TenantBackends) > 0 {
				tenantBals := make(map[string]loadbalancer.Balancer, len(routeCfg.TenantBackends))
				for tid, tBackends := range routeCfg.TenantBackends {
					var tBacks []*loadbalancer.Backend
					for _, b := range tBackends {
						weight := b.Weight
						if weight == 0 {
							weight = 1
						}
						tbe := &loadbalancer.Backend{URL: b.URL, Weight: weight, Healthy: true}
						tbe.InitParsedURL()
						tBacks = append(tBacks, tbe)
						g.healthChecker.AddBackend(upstreamHealthCheck(b.URL, g.config.HealthCheck, nil, b.HealthCheck))
					}
					tenantBals[tid] = createBalancerForBackends(routeCfg, tBacks)
				}
				bal = loadbalancer.NewTenantAwareBalancer(bal, tenantBals)
			}
			if routeCfg.SessionAffinity.Enabled {
				bal = loadbalancer.NewSessionAffinityBalancer(bal, routeCfg.SessionAffinity)
			}
			routeProxy = proxy.NewRouteProxyWithBalancer(g.proxy, route, bal)
		}
		g.storeRouteProxy(routeCfg.ID, routeProxy)

		// Wire shared retry budget pool if configured
		if routeCfg.RetryPolicy.BudgetPool != "" {
			if pool, ok := g.budgetPools[routeCfg.RetryPolicy.BudgetPool]; ok {
				routeProxy.SetRetryBudget(pool)
			}
		}
	}

	// Set up rate limiting (unique setup signature, not in feature loop)
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
		g.rateLimiters.AddRouteTiered(routeCfg.ID, ratelimit.TieredConfig{
			Tiers:       tiers,
			TierKey:     routeCfg.RateLimit.TierKey,
			DefaultTier: routeCfg.RateLimit.DefaultTier,
			KeyFn:       keyFn,
		})
	} else if routeCfg.RateLimit.Enabled || routeCfg.RateLimit.Rate > 0 {
		if routeCfg.RateLimit.Mode == "distributed" && g.redisClient != nil {
			g.rateLimiters.AddRouteDistributed(routeCfg.ID, ratelimit.RedisLimiterConfig{
				Client: g.redisClient,
				Prefix: "gw:rl:" + routeCfg.ID + ":",
				Rate:   routeCfg.RateLimit.Rate,
				Period: routeCfg.RateLimit.Period,
				Burst:  routeCfg.RateLimit.Burst,
				PerIP:  routeCfg.RateLimit.PerIP,
				Key:    routeCfg.RateLimit.Key,
			})
		} else if routeCfg.RateLimit.Algorithm == "sliding_window" {
			g.rateLimiters.AddRouteSlidingWindow(routeCfg.ID, ratelimit.Config{
				Rate:   routeCfg.RateLimit.Rate,
				Period: routeCfg.RateLimit.Period,
				Burst:  routeCfg.RateLimit.Burst,
				PerIP:  routeCfg.RateLimit.PerIP,
				Key:    routeCfg.RateLimit.Key,
			})
		} else {
			g.rateLimiters.AddRoute(routeCfg.ID, ratelimit.Config{
				Rate:   routeCfg.RateLimit.Rate,
				Period: routeCfg.RateLimit.Period,
				Burst:  routeCfg.RateLimit.Burst,
				PerIP:  routeCfg.RateLimit.PerIP,
				Key:    routeCfg.RateLimit.Key,
			})
		}
	}

	// Set up gRPC handler (unique setup signature, not in feature loop)
	if routeCfg.GRPC.Enabled {
		g.grpcHandlers.AddRoute(routeCfg.ID, routeCfg.GRPC)
	}

	// Set up protocol translator (replaces RouteProxy as innermost handler; blocked by echo validation)
	if routeCfg.Protocol.Type != "" && routeProxy != nil {
		bal := routeProxy.GetBalancer()
		if err := g.translators.AddRoute(routeCfg.ID, routeCfg.Protocol, bal); err != nil {
			return fmt.Errorf("protocol translator: route %s: %w", routeCfg.ID, err)
		}
	}

	// Set up all features generically
	for _, f := range g.features {
		if err := f.Setup(routeCfg.ID, routeCfg); err != nil {
			return fmt.Errorf("feature %s: route %s: %w", f.Name(), routeCfg.ID, err)
		}
	}

	// Set up sequential handler (needs transport from proxy's transport pool)
	if routeCfg.Sequential.Enabled {
		transport := g.proxy.GetTransportPool().Get(routeCfg.Upstream)
		ch := routeCfg.CompletionHeader || g.config.CompletionHeader
		if err := g.sequentialHandlers.AddRoute(routeCfg.ID, routeCfg.Sequential, transport, ch); err != nil {
			return fmt.Errorf("sequential: route %s: %w", routeCfg.ID, err)
		}
	}

	// Set up aggregate handler (needs transport from proxy's transport pool)
	if routeCfg.Aggregate.Enabled {
		transport := g.proxy.GetTransportPool().Get(routeCfg.Upstream)
		ch := routeCfg.CompletionHeader || g.config.CompletionHeader
		if err := g.aggregateHandlers.AddRoute(routeCfg.ID, routeCfg.Aggregate, transport, ch); err != nil {
			return fmt.Errorf("aggregate: route %s: %w", routeCfg.ID, err)
		}
	}

	// Set up Lambda backend handler
	if routeCfg.Lambda.Enabled {
		if err := g.lambdaHandlers.AddRoute(routeCfg.ID, routeCfg.Lambda); err != nil {
			return fmt.Errorf("lambda: route %s: %w", routeCfg.ID, err)
		}
	}

	// Set up AMQP backend handler
	if routeCfg.AMQP.Enabled {
		if err := g.amqpHandlers.AddRoute(routeCfg.ID, routeCfg.AMQP); err != nil {
			return fmt.Errorf("amqp: route %s: %w", routeCfg.ID, err)
		}
	}

	// Set up PubSub backend handler
	if routeCfg.PubSub.Enabled {
		if err := g.pubsubHandlers.AddRoute(routeCfg.ID, routeCfg.PubSub); err != nil {
			return fmt.Errorf("pubsub: route %s: %w", routeCfg.ID, err)
		}
	}

	// Override per-try timeout with backend timeout when configured
	if routeCfg.TimeoutPolicy.Backend > 0 && routeProxy != nil {
		routeProxy.SetPerTryTimeout(routeCfg.TimeoutPolicy.Backend)
	}

	// Set up canary controller (needs WeightedBalancer, only available after route proxy creation)
	if routeCfg.Canary.Enabled {
		if wb, ok := routeProxy.GetBalancer().(*loadbalancer.WeightedBalancer); ok {
			if err := g.canaryControllers.AddRoute(routeCfg.ID, routeCfg.Canary, wb); err != nil {
				return fmt.Errorf("canary: route %s: %w", routeCfg.ID, err)
			}
			if routeCfg.Canary.AutoStart {
				if ctrl := g.canaryControllers.GetController(routeCfg.ID); ctrl != nil {
					if err := ctrl.Start(); err != nil {
						return fmt.Errorf("canary auto-start: route %s: %w", routeCfg.ID, err)
					}
				}
			}
		}
	}

	// Set up blue-green controller (needs WeightedBalancer, only available after route proxy creation)
	if routeCfg.BlueGreen.Enabled {
		if wb, ok := routeProxy.GetBalancer().(*loadbalancer.WeightedBalancer); ok {
			g.blueGreenControllers.AddRoute(routeCfg.ID, routeCfg.BlueGreen, wb, g.healthChecker)
		}
	}

	// Set up A/B test (needs WeightedBalancer, only available after route proxy creation)
	if routeCfg.ABTest.Enabled {
		if wb, ok := routeProxy.GetBalancer().(*loadbalancer.WeightedBalancer); ok {
			g.abTests.AddRoute(routeCfg.ID, routeCfg.ABTest, wb)
		}
	}

	// Set up outlier detection (needs Balancer, only available after route proxy creation)
	if routeCfg.OutlierDetection.Enabled {
		g.outlierDetectors.AddRoute(routeCfg.ID, routeCfg.OutlierDetection, routeProxy.GetBalancer())
	}

	// Set up backend backpressure (needs Balancer, only available after route proxy creation)
	if routeCfg.Backpressure.Enabled && routeProxy != nil {
		g.backpressureHandlers.AddRoute(routeCfg.ID, routeCfg.Backpressure, routeProxy.GetBalancer())
	}

	// Set up SSE fan-out hub (needs balancer from routeProxy)
	if routeCfg.SSE.Enabled && routeCfg.SSE.Fanout.Enabled && routeProxy != nil {
		hub := sse.NewHub(routeCfg.SSE.Fanout, routeProxy.GetBalancer())
		if sh := g.sseHandlers.GetHandler(routeCfg.ID); sh != nil {
			sh.SetHub(hub)
			hub.Start()
		}
	}

	// Build the per-route middleware pipeline handler
	handler := g.buildRouteHandler(routeCfg.ID, routeCfg, route, routeProxy)
	g.storeRouteHandler(routeCfg.ID, handler)

	return nil
}

// createBalancer creates a load balancer for the given route config and backends.
func createBalancer(cfg config.RouteConfig, backends []*loadbalancer.Backend) loadbalancer.Balancer {
	return createBalancerForBackends(cfg, backends)
}

// createBalancerForBackends creates a load balancer for the given backend set using route LB config.
func createBalancerForBackends(cfg config.RouteConfig, backends []*loadbalancer.Backend) loadbalancer.Balancer {
	switch cfg.LoadBalancer {
	case "least_conn":
		return loadbalancer.NewLeastConnections(backends)
	case "consistent_hash":
		return loadbalancer.NewConsistentHash(backends, cfg.ConsistentHash)
	case "least_response_time":
		return loadbalancer.NewLeastResponseTime(backends)
	default:
		return loadbalancer.NewRoundRobin(backends)
	}
}

// buildRouteHandler constructs the per-route middleware pipeline.
// Chain ordering matches CLAUDE.md serveHTTP flow exactly.
func (g *Gateway) buildRouteHandler(routeID string, cfg config.RouteConfig, route *router.Route, rp *proxy.RouteProxy) http.Handler {
	skipBody := cfg.Passthrough
	isGRPC := cfg.GRPC.Enabled
	routeEngine := g.routeRules.GetEngine(routeID)

	// Pre-compile body transforms (skip for passthrough routes)
	var reqBodyTransform, respBodyTransform *transform.CompiledBodyTransform
	if !skipBody && route.Transform.Request.Body.IsActive() {
		reqBodyTransform, _ = transform.NewCompiledBodyTransform(route.Transform.Request.Body)
	}
	if !skipBody && route.Transform.Response.Body.IsActive() {
		respBodyTransform, _ = transform.NewCompiledBodyTransform(route.Transform.Response.Body)
	}

	// Middleware chain in order. Each function returns a middleware or nil to skip.
	// Order matches CLAUDE.md serveHTTP flow exactly — do not reorder.
	mws := [...]func() middleware.Middleware{
		/* 1    */ func() middleware.Middleware { return metricsMW(g.metricsCollector, routeID) },
		/* 1.5  */ func() middleware.Middleware {
			if ctrl := g.canaryControllers.GetController(routeID); ctrl != nil { return canaryObserverMW(ctrl) }
			if bg := g.blueGreenControllers.GetController(routeID); bg != nil { return blueGreenObserverMW(bg) }
			if ab := g.abTests.GetTest(routeID); ab != nil { return abTestObserverMW(ab) }
			return nil
		},
		/* 2    */ func() middleware.Middleware {
			rf := g.ipFilters.GetFilter(routeID)
			if g.globalIPFilter != nil || rf != nil { return ipFilterMW(g.globalIPFilter, rf) }; return nil
		},
		/* 2.5  */ func() middleware.Middleware {
			rg := g.geoFilters.GetGeo(routeID)
			if g.globalGeo != nil || rg != nil { return geoMW(g.globalGeo, rg) }; return nil
		},
		/* 2.75 */ func() middleware.Middleware {
			if m := g.maintenanceHandlers.GetMaintenance(routeID); m != nil { return m.Middleware() }; return nil
		},
		/* 2.8  */ func() middleware.Middleware {
			if bd := g.botDetectors.GetDetector(routeID); bd != nil { return bd.Middleware() }; return nil
		},
		/* 2.85 */ func() middleware.Middleware {
			bl := g.ipBlocklists.GetBlocklist(routeID)
			if g.globalBlocklist != nil || bl != nil { return ipBlocklistMW(g.globalBlocklist, bl) }; return nil
		},
		/* 2.87 */ func() middleware.Middleware {
			if v := g.clientMTLSVerifiers.GetVerifier(routeID); v != nil { return v.Middleware() }; return nil
		},
		/* 3    */ func() middleware.Middleware {
			if ch := g.corsHandlers.GetHandler(routeID); ch != nil && ch.IsEnabled() { return ch.Middleware() }; return nil
		},
		/* 4    */ func() middleware.Middleware { return varContextMW(routeID) },
		/* 4.05 */ func() middleware.Middleware {
			if sh := g.securityHeaders.GetHeaders(routeID); sh != nil { return sh.Middleware() }; return nil
		},
		/* 4.07 */ func() middleware.Middleware {
			if cdn := g.cdnHeaders.GetHandler(routeID); cdn != nil { return cdn.Middleware() }; return nil
		},
		/* 4.1  */ func() middleware.Middleware {
			if ep := g.errorPages.GetErrorPages(routeID); ep != nil { return ep.Middleware() }; return nil
		},
		/* 4.25 */ func() middleware.Middleware {
			if al := g.accessLogConfigs.GetConfig(routeID); al != nil { return al.Middleware() }; return nil
		},
		/* 4.3  */ func() middleware.Middleware {
			if al := g.auditLoggers.GetLogger(routeID); al != nil { return al.Middleware() }; return nil
		},
		/* 4.5  */ func() middleware.Middleware {
			if ver := g.versioners.GetVersioner(routeID); ver != nil { return ver.Middleware() }; return nil
		},
		/* 4.75 */ func() middleware.Middleware {
			if ct := g.timeoutConfigs.GetTimeout(routeID); ct != nil { return ct.Middleware() }; return nil
		},
		/* 5    */ func() middleware.Middleware { return g.rateLimiters.GetMiddleware(routeID) },
		/* 5.25 */ func() middleware.Middleware {
			if sa := g.spikeArresters.GetArrester(routeID); sa != nil { return sa.Middleware() }; return nil
		},
		/* 5.3  */ func() middleware.Middleware {
			if qe := g.quotaEnforcers.GetEnforcer(routeID); qe != nil { return qe.Middleware() }; return nil
		},
		/* 5.5  */ func() middleware.Middleware {
			if t := g.throttlers.GetThrottler(routeID); t != nil { return t.Middleware() }; return nil
		},
		/* 5.75 */ func() middleware.Middleware {
			if rq := g.requestQueues.GetQueue(routeID); rq != nil { return rq.Middleware() }; return nil
		},
		/* 6    */ func() middleware.Middleware {
			if route.Auth.Required { return authMW(g, route.Auth) }; return nil
		},
		/* 6.05 */ func() middleware.Middleware {
			if g.tokenChecker != nil && route.Auth.Required { return g.tokenChecker.Middleware() }; return nil
		},
		/* 6.07 */ func() middleware.Middleware {
			if te := g.tokenExchangers.GetExchanger(routeID); te != nil { return te.Middleware() }; return nil
		},
		/* 6.15 */ func() middleware.Middleware {
			if cp := g.claimsPropagators.GetPropagator(routeID); cp != nil { return cp.Middleware() }; return nil
		},
		/* 6.25 */ func() middleware.Middleware {
			if ea := g.extAuths.GetAuth(routeID); ea != nil { return ea.Middleware() }; return nil
		},
		/* 6.3  */ func() middleware.Middleware {
			if nc := g.nonceCheckers.GetChecker(routeID); nc != nil { return nc.Middleware() }; return nil
		},
		/* 6.35 */ func() middleware.Middleware {
			if cp := g.csrfProtectors.GetProtector(routeID); cp != nil { return cp.Middleware() }; return nil
		},
		/* 6.37 */ func() middleware.Middleware {
			if v := g.inboundVerifiers.GetVerifier(routeID); v != nil { return v.Middleware() }; return nil
		},
		/* 6.4  */ func() middleware.Middleware {
			if ih := g.idempotencyHandlers.GetHandler(routeID); ih != nil { return ih.Middleware() }; return nil
		},
		/* 6.45 */ func() middleware.Middleware {
			if dh := g.dedupHandlers.GetHandler(routeID); dh != nil { return dh.Middleware() }; return nil
		},
		/* 6.5  */ func() middleware.Middleware {
			if g.priorityAdmitter == nil { return nil }
			if pcfg, ok := g.priorityConfigs.GetConfig(routeID); ok { return priorityMW(g.priorityAdmitter, pcfg) }; return nil
		},
		/* 6.55 */ func() middleware.Middleware {
			if bp := g.baggagePropagators.GetPropagator(routeID); bp != nil { return bp.Middleware() }; return nil
		},
		/* 6.6  */ func() middleware.Middleware {
			if g.tenantManager == nil { return nil }
			return g.tenantManager.Middleware(cfg.Tenant.Allowed, cfg.Tenant.Required)
		},
		/* 7    */ func() middleware.Middleware {
			hasReq := (g.globalRules != nil && g.globalRules.HasRequestRules()) ||
				(routeEngine != nil && routeEngine.HasRequestRules())
			if hasReq { return requestRulesMW(g.globalRules, routeEngine) }; return nil
		},
		/* 7.25 */ func() middleware.Middleware {
			if wh := g.wafHandlers.GetWAF(routeID); wh != nil { return wh.Middleware() }; return nil
		},
		/* 7.5  */ func() middleware.Middleware {
			if fi := g.faultInjectors.GetInjector(routeID); fi != nil { return fi.Middleware() }; return nil
		},
		/* 7.6  */ func() middleware.Middleware {
			if rec := g.trafficReplay.GetRecorder(routeID); rec != nil { return rec.RecordingMiddleware() }; return nil
		},
		/* 7.75 */ func() middleware.Middleware {
			if mh := g.mockHandlers.GetHandler(routeID); mh != nil { return mh.Middleware() }; return nil
		},
		/* 7.8  */ func() middleware.Middleware {
			if ls := g.luaScripters.GetScript(routeID); ls != nil { return ls.RequestMiddleware() }; return nil
		},
		/* 7.85 */ func() middleware.Middleware {
			if wc := g.wasmPlugins.GetChain(routeID); wc != nil { return wc.RequestMiddleware() }; return nil
		},
		/* 8    */ func() middleware.Middleware {
			if !skipBody && route.MaxBodySize > 0 { return bodyLimitMW(route.MaxBodySize) }; return nil
		},
		/* 8.25 */ func() middleware.Middleware {
			if skipBody { return nil }
			if d := g.decompressors.GetDecompressor(routeID); d != nil && d.IsEnabled() { return d.Middleware() }; return nil
		},
		/* 8.5  */ func() middleware.Middleware {
			if skipBody { return nil }
			if bw := g.bandwidthLimiters.GetLimiter(routeID); bw != nil { return bw.Middleware() }; return nil
		},
		/* 8.6  */ func() middleware.Middleware {
			if skipBody { return nil }
			if fe := g.fieldEncryptors.GetEncryptor(routeID); fe != nil { return fe.Middleware() }; return nil
		},
		/* 9    */ func() middleware.Middleware {
			if skipBody { return nil }
			if v := g.validators.GetValidator(routeID); v != nil && v.IsEnabled() { return v.Middleware() }; return nil
		},
		/* 9.1  */ func() middleware.Middleware {
			if skipBody { return nil }
			if ov := g.openapiValidators.GetValidator(routeID); ov != nil { return openapiRequestMW(ov) }; return nil
		},
		/* 9.5  */ func() middleware.Middleware {
			if skipBody { return nil }
			if gql := g.graphqlParsers.GetParser(routeID); gql != nil { return gql.Middleware() }; return nil
		},
		/* 10   */ func() middleware.Middleware {
			if route.WebSocket.Enabled {
				return websocketMW(g.wsProxy, func() loadbalancer.Balancer { return rp.GetBalancer() })
			}; return nil
		},
		/* 10.5 */ func() middleware.Middleware {
			if sh := g.sseHandlers.GetHandler(routeID); sh != nil { return sh.Middleware() }; return nil
		},
		/* 11   */ func() middleware.Middleware {
			if skipBody { return nil }
			if ch := g.caches.GetHandler(routeID); ch != nil { return cacheMW(ch, g.metricsCollector, routeID) }; return nil
		},
		/* 11.5 */ func() middleware.Middleware {
			if skipBody { return nil }
			if c := g.coalescers.GetCoalescer(routeID); c != nil { return c.Middleware() }; return nil
		},
		/* 12   */ func() middleware.Middleware {
			if cb := g.circuitBreakers.GetBreaker(routeID); cb != nil { return circuitBreakerMW(cb, isGRPC) }; return nil
		},
		/* 12.25*/ func() middleware.Middleware {
			if det := g.outlierDetectors.GetDetector(routeID); det != nil { return det.Middleware() }; return nil
		},
		/* 12.5 */ func() middleware.Middleware {
			if al := g.adaptiveLimiters.GetLimiter(routeID); al != nil { return adaptiveConcurrencyMW(al) }; return nil
		},
		/* 12.55*/ func() middleware.Middleware {
			if bp := g.backpressureHandlers.GetHandler(routeID); bp != nil { return bp.Middleware() }; return nil
		},
		/* 12.6 */ func() middleware.Middleware {
			if pl := g.proxyRateLimiters.GetLimiter(routeID); pl != nil { return pl.Middleware() }; return nil
		},
		/* 13   */ func() middleware.Middleware {
			if skipBody { return nil }
			if c := g.compressors.GetCompressor(routeID); c != nil && c.IsEnabled() { return c.Middleware() }; return nil
		},
		/* 13.5 */ func() middleware.Middleware {
			if skipBody { return nil }
			if rl := g.responseLimiters.GetLimiter(routeID); rl != nil && rl.IsEnabled() { return rl.Middleware() }; return nil
		},
		/* 14   */ func() middleware.Middleware {
			hasResp := (g.globalRules != nil && g.globalRules.HasResponseRules()) ||
				(routeEngine != nil && routeEngine.HasResponseRules())
			if hasResp { return responseRulesMW(g.globalRules, routeEngine) }; return nil
		},
		/* 15   */ func() middleware.Middleware {
			if mh := g.mirrors.GetMirror(routeID); mh != nil && mh.IsEnabled() { return mh.Middleware() }; return nil
		},
		/* 15.5 */ func() middleware.Middleware {
			if rp == nil { return nil }
			if wb, ok := rp.GetBalancer().(*loadbalancer.WeightedBalancer); ok && wb.HasStickyPolicy() {
				return trafficGroupMW(wb.GetStickyPolicy())
			}; return nil
		},
		/* 15.55*/ func() middleware.Middleware {
			if rp == nil { return nil }
			if sa, ok := rp.GetBalancer().(*loadbalancer.SessionAffinityBalancer); ok {
				return sessionAffinityMW(sa)
			}; return nil
		},
		/* 16   */ func() middleware.Middleware {
			return requestTransformMW(route, g.grpcHandlers.GetHandler(routeID), reqBodyTransform)
		},
		/* 16.05*/ func() middleware.Middleware {
			if bg := g.bodyGenerators.GetGenerator(routeID); bg != nil { return bg.Middleware() }; return nil
		},
		/* 16.07*/ func() middleware.Middleware {
			if mc := g.modifierChains.GetChain(routeID); mc != nil { return mc.Middleware() }; return nil
		},
		/* 16.1 */ func() middleware.Middleware {
			if pf := g.paramForwarders.GetForwarder(routeID); pf != nil { return pf.Middleware() }; return nil
		},
		/* 16.25*/ func() middleware.Middleware {
			if ba := g.backendAuths.GetProvider(routeID); ba != nil { return ba.Middleware() }; return nil
		},
		/* 16.5 */ func() middleware.Middleware {
			if s := g.backendSigners.GetSigner(routeID); s != nil { return s.Middleware() }; return nil
		},
		/* 17   */ func() middleware.Middleware {
			if !skipBody && respBodyTransform != nil { return transform.ResponseBodyTransformMiddleware(respBodyTransform) }; return nil
		},
		/* 17.05*/ func() middleware.Middleware {
			if wc := g.wasmPlugins.GetChain(routeID); wc != nil { return wc.ResponseMiddleware() }; return nil
		},
		/* 17.1 */ func() middleware.Middleware {
			if ls := g.luaScripters.GetScript(routeID); ls != nil { return ls.ResponseMiddleware() }; return nil
		},
		/* 17.15*/ func() middleware.Middleware {
			if skipBody { return nil }
			if jm := g.jmespathHandlers.GetJMESPath(routeID); jm != nil { return jm.Middleware() }; return nil
		},
		/* 17.25*/ func() middleware.Middleware {
			if sm := g.statusMappers.GetMapper(routeID); sm != nil { return sm.Middleware() }; return nil
		},
		/* 17.3 */ func() middleware.Middleware {
			if skipBody { return nil }
			if cr := g.contentReplacers.GetReplacer(routeID); cr != nil { return cr.Middleware() }; return nil
		},
		/* 17.31*/ func() middleware.Middleware {
			if skipBody { return nil }
			if pr := g.piiRedactors.GetRedactor(routeID); pr != nil { return pr.Middleware() }; return nil
		},
		/* 17.32*/ func() middleware.Middleware {
			if skipBody { return nil }
			if fr := g.fieldReplacers.GetReplacer(routeID); fr != nil { return fr.Middleware() }; return nil
		},
		/* 17.35*/ func() middleware.Middleware {
			if skipBody { return nil }
			if rbg := g.respBodyGenerators.GetGenerator(routeID); rbg != nil { return rbg.Middleware() }; return nil
		},
		/* 17.38*/ func() middleware.Middleware {
			if eh := g.errorHandlers.GetHandler(routeID); eh != nil { return eh.Middleware() }; return nil
		},
		/* 17.4 */ func() middleware.Middleware {
			if skipBody { return nil }
			if cn := g.contentNegotiators.GetNegotiator(routeID); cn != nil { return cn.Middleware() }; return nil
		},
	}

	chain := middleware.NewBuilderWithCap(len(mws))
	for _, build := range mws {
		if mw := build(); mw != nil {
			chain = chain.Use(mw)
		}
	}

	// Innermost handler: aggregate, sequential, echo, static, translator, or proxy
	var innermost http.Handler
	if aggH := g.aggregateHandlers.GetHandler(routeID); aggH != nil {
		innermost = aggH
	} else if seqH := g.sequentialHandlers.GetHandler(routeID); seqH != nil {
		innermost = seqH
	} else if cfg.Echo {
		innermost = proxy.NewEchoHandler(routeID)
	} else if sh := g.staticFiles.GetHandler(routeID); sh != nil {
		innermost = sh
	} else if fcgiH := g.fastcgiHandlers.GetHandler(routeID); fcgiH != nil {
		innermost = fcgiH
	} else if translatorHandler := g.translators.GetHandler(routeID); translatorHandler != nil {
		innermost = translatorHandler
	} else if lambdaH := g.lambdaHandlers.GetHandler(routeID); lambdaH != nil {
		innermost = lambdaH
	} else if amqpH := g.amqpHandlers.GetHandler(routeID); amqpH != nil {
		innermost = amqpH
	} else if pubsubH := g.pubsubHandlers.GetHandler(routeID); pubsubH != nil {
		innermost = pubsubH
	} else {
		innermost = rp
	}

	// 17.55. backendEncodingMW — wraps innermost handler directly
	if be := g.backendEncoders.GetEncoder(routeID); be != nil {
		innermost = be.Middleware()(innermost)
	}

	// 17.56. isCollectionMW — wraps array responses as {"collection_key": [...]}
	if cfg.BackendResponse.IsCollection {
		innermost = isCollectionMW(cfg.BackendResponse.CollectionKey)(innermost)
	}

	// 17.5 responseValidationMW — wraps innermost (closest to proxy)
	if !skipBody {
		respValidator := g.validators.GetValidator(routeID)
		openapiV := g.openapiValidators.GetValidator(routeID)
		hasRespValidation := (respValidator != nil && respValidator.HasResponseSchema()) ||
			(openapiV != nil && openapiV.ValidatesResponse())
		if hasRespValidation {
			innermost = responseValidationMW(respValidator, openapiV)(innermost)
		}
	}

	return chain.Handler(innermost)
}

// mergeHealthCheckConfig builds a health.Backend from global and per-backend config.
func mergeHealthCheckConfig(backendURL string, global config.HealthCheckConfig, perBackend *config.HealthCheckConfig) health.Backend {
	b := health.Backend{URL: backendURL}

	// Start from global
	b.HealthPath = global.Path
	b.Method = global.Method
	b.Interval = global.Interval
	b.Timeout = global.Timeout
	b.HealthyAfter = global.HealthyAfter
	b.UnhealthyAfter = global.UnhealthyAfter

	// Parse global expected status
	for _, s := range global.ExpectedStatus {
		if r, err := health.ParseStatusRange(s); err == nil {
			b.ExpectedStatus = append(b.ExpectedStatus, r)
		}
	}

	// Override with per-backend values where non-zero/non-empty
	if perBackend != nil {
		if perBackend.Path != "" {
			b.HealthPath = perBackend.Path
		}
		if perBackend.Method != "" {
			b.Method = perBackend.Method
		}
		if perBackend.Interval > 0 {
			b.Interval = perBackend.Interval
		}
		if perBackend.Timeout > 0 {
			b.Timeout = perBackend.Timeout
		}
		if perBackend.HealthyAfter > 0 {
			b.HealthyAfter = perBackend.HealthyAfter
		}
		if perBackend.UnhealthyAfter > 0 {
			b.UnhealthyAfter = perBackend.UnhealthyAfter
		}
		if len(perBackend.ExpectedStatus) > 0 {
			b.ExpectedStatus = nil
			for _, s := range perBackend.ExpectedStatus {
				if r, err := health.ParseStatusRange(s); err == nil {
					b.ExpectedStatus = append(b.ExpectedStatus, r)
				}
			}
		}
	}

	return b
}

// watchService watches for service changes from registry
func (g *Gateway) watchService(routeID, serviceName string, tags []string) {
	ctx, cancel := context.WithCancel(context.Background())

	g.mu.Lock()
	if existingCancel, ok := g.watchCancels[routeID]; ok {
		existingCancel()
	}
	g.watchCancels[routeID] = cancel
	g.mu.Unlock()

	go func() {
		ch, err := g.registry.Watch(ctx, serviceName)
		if err != nil {
			logging.Error("Failed to watch service",
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

				// Filter by tags if specified
				var filtered []*registry.Service
				for _, svc := range services {
					if hasAllTags(svc.Tags, tags) {
						filtered = append(filtered, svc)
					}
				}

				// Convert to backends
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

				// Update route proxy
				rp, ok := (*g.routeProxies.Load())[routeID]
				if ok {
					rp.UpdateBackends(backends)
					logging.Info("Updated backends for route",
						zap.String("route", routeID),
						zap.Int("services", len(backends)),
					)
				}
			}
		}
	}()
}

// hasAllTags checks if service has all required tags
func hasAllTags(serviceTags, requiredTags []string) bool {
	if len(requiredTags) == 0 {
		return true
	}

	tagSet := make(map[string]bool)
	for _, t := range serviceTags {
		tagSet[t] = true
	}

	for _, t := range requiredTags {
		if !tagSet[t] {
			return false
		}
	}
	return true
}

// updateBackendHealth updates backend health status based on health checker
func (g *Gateway) updateBackendHealth(url string, status health.Status) {
	healthy := status == health.StatusHealthy

	for _, rp := range *g.routeProxies.Load() {
		if healthy {
			rp.GetBalancer().MarkHealthy(url)
		} else {
			rp.GetBalancer().MarkUnhealthy(url)
		}
	}
}

// Handler returns the main HTTP handler
func (g *Gateway) Handler() http.Handler {
	chain := middleware.NewBuilderWithCap(16).
		Use(middleware.Recovery())

	// Real IP extraction from trusted proxies (before everything else)
	if g.realIPExtractor != nil {
		chain = chain.Use(g.realIPExtractor.Middleware)
	}

	// HTTPS redirect (after RealIP, before RequestID)
	if g.httpsRedirect != nil {
		chain = chain.Use(g.httpsRedirect.Middleware)
	}

	// Allowed hosts validation (after HTTPS redirect, before RequestID)
	if g.allowedHosts != nil {
		chain = chain.Use(g.allowedHosts.Middleware)
	}

	chain = chain.Use(middleware.RequestID())

	// Load shedding (after RequestID, before service rate limit)
	if g.loadShedder != nil {
		chain = chain.Use(g.loadShedder.Middleware())
	}

	// Service-level rate limit (after RequestID, before Alt-Svc)
	if g.serviceLimiter != nil {
		chain = chain.Use(g.serviceLimiter.Middleware())
	}

	// Alt-Svc: advertise HTTP/3 on HTTP/1+2 responses
	if g.http3AltSvcPort != "" {
		chain = chain.Use(altsvc.Middleware(g.http3AltSvcPort))
	}

	chain = chain.Use(mtls.Middleware())

	if g.tracer != nil {
		chain = chain.Use(g.tracer.Middleware())
	}

	chain = chain.Use(middleware.LoggingWithConfig(middleware.LoggingConfig{
		Format: g.config.Logging.Format,
		JSON:   g.config.Logging.Level == "json",
	}))

	return chain.Handler(http.HandlerFunc(g.serveHTTP))
}

// serveHTTP handles incoming requests by dispatching to the per-route handler pipeline.
func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	// Debug endpoint intercept (before route matching)
	if g.debugHandler != nil && g.debugHandler.Matches(r.URL.Path) {
		g.debugHandler.ServeHTTP(w, r)
		return
	}

	match := g.router.Match(r)
	if match == nil {
		errors.ErrNotFound.WriteJSON(w)
		return
	}
	defer router.ReleaseMatch(match)

	// Set path params directly on the existing varCtx (already in context from RequestID middleware).
	varCtx := variables.GetFromRequest(r)
	varCtx.PathParams = match.PathParams

	handler, ok := (*g.routeHandlers.Load())[match.Route.ID]
	if !ok {
		errors.ErrInternalServer.WithDetails("Route handler not found").WriteJSON(w)
		return
	}

	handler.ServeHTTP(w, r)
}

// authenticate handles authentication for a request
func (g *Gateway) authenticate(w http.ResponseWriter, r *http.Request, methods []string) bool {
	// If no specific methods, try all available
	if len(methods) == 0 {
		methods = []string{"jwt", "api_key", "oauth"}
	}

	var identity *variables.Identity
	var err error

	for _, method := range methods {
		switch method {
		case "jwt":
			if g.jwtAuth != nil && g.jwtAuth.IsEnabled() {
				identity, err = g.jwtAuth.Authenticate(r)
				if err == nil {
					break
				}
			}
		case "api_key":
			if g.apiKeyAuth != nil && g.apiKeyAuth.IsEnabled() {
				identity, err = g.apiKeyAuth.Authenticate(r)
				if err == nil {
					break
				}
			}
		case "oauth":
			if g.oauthAuth != nil && g.oauthAuth.IsEnabled() {
				identity, err = g.oauthAuth.Authenticate(r)
				if err == nil {
					break
				}
			}
		}

		if identity != nil {
			break
		}
	}

	if identity == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="api", API-Key`)
		errors.ErrUnauthorized.WriteJSON(w)
		return false
	}

	// Add identity to context
	varCtx := variables.GetFromRequest(r)
	varCtx.Identity = identity

	return true
}

// Close closes the gateway and releases resources
func (g *Gateway) Close() error {
	// Cancel all watchers
	g.mu.Lock()
	for _, cancel := range g.watchCancels {
		cancel()
	}
	g.watchCancels = make(map[string]context.CancelFunc)
	g.mu.Unlock()

	// Stop health checker
	g.healthChecker.Stop()

	// Close JWKS providers
	if g.jwtAuth != nil {
		g.jwtAuth.Close()
	}

	// Close tracer
	if g.tracer != nil {
		g.tracer.Close()
	}

	// Close tenant manager
	if g.tenantManager != nil {
		g.tenantManager.Close()
	}

	// Close Redis client
	if g.redisClient != nil {
		g.redisClient.Close()
	}

	// Close webhook dispatcher
	if g.webhookDispatcher != nil {
		g.webhookDispatcher.Close()
	}

	// Stop canary controllers
	g.canaryControllers.StopAll()

	// Stop adaptive concurrency limiters
	g.adaptiveLimiters.StopAll()

	// Stop outlier detectors
	g.outlierDetectors.StopAll()

	// Close nonce checkers
	g.nonceCheckers.CloseAll()

	// Close idempotency handlers
	g.idempotencyHandlers.CloseAll()

	// Close token revocation checker
	if g.tokenChecker != nil {
		g.tokenChecker.Close()
	}

	// Close load shedder
	if g.loadShedder != nil {
		g.loadShedder.Close()
	}

	// Close backpressure handlers
	g.backpressureHandlers.CloseAll()

	// Close audit loggers
	g.auditLoggers.CloseAll()

	// Close ext auth clients
	g.extAuths.CloseAll()

	// Close geo provider
	if g.geoProvider != nil {
		g.geoProvider.Close()
	}

	// Stop SSE fan-out hubs
	g.sseHandlers.StopAllHubs()

	// Close WASM plugin runtime and pools
	g.wasmPlugins.Close(context.Background())

	// Close protocol translators
	g.translators.Close()

	// Close registry
	if g.registry != nil {
		return g.registry.Close()
	}

	return nil
}

// GetRouter returns the router
func (g *Gateway) GetRouter() *router.Router {
	return g.router
}

// GetRegistry returns the registry
func (g *Gateway) GetRegistry() registry.Registry {
	return g.registry
}

// GetHealthChecker returns the health checker
func (g *Gateway) GetHealthChecker() *health.Checker {
	return g.healthChecker
}

// GetCircuitBreakers returns the circuit breaker manager
func (g *Gateway) GetCircuitBreakers() *circuitbreaker.BreakerByRoute {
	return g.circuitBreakers
}

// GetCaches returns the cache manager
func (g *Gateway) GetCaches() *cache.CacheByRoute {
	return g.caches
}

// GetRetryMetrics returns the retry metrics per route
func (g *Gateway) GetRetryMetrics() map[string]*retry.RouteRetryMetrics {
	result := make(map[string]*retry.RouteRetryMetrics)
	for routeID, rp := range *g.routeProxies.Load() {
		if m := rp.GetRetryMetrics(); m != nil {
			result[routeID] = m
		}
	}
	return result
}

// GetMetricsCollector returns the metrics collector
func (g *Gateway) GetMetricsCollector() *metrics.Collector {
	return g.metricsCollector
}

// GetGlobalRules returns the global rules engine (may be nil).
func (g *Gateway) GetGlobalRules() *rules.RuleEngine {
	return g.globalRules
}

// GetRouteRules returns the per-route rules manager.
func (g *Gateway) GetRouteRules() *rules.RulesByRoute {
	return g.routeRules
}

// GetTranslators returns the protocol translator manager.
func (g *Gateway) GetTranslators() *protocol.TranslatorByRoute {
	return g.translators
}

// GetThrottlers returns the throttle manager.
func (g *Gateway) GetThrottlers() *trafficshape.ThrottleByRoute {
	return g.throttlers
}

// GetBandwidthLimiters returns the bandwidth limiter manager.
func (g *Gateway) GetBandwidthLimiters() *trafficshape.BandwidthByRoute {
	return g.bandwidthLimiters
}

// GetPriorityAdmitter returns the priority admitter (may be nil).
func (g *Gateway) GetPriorityAdmitter() *trafficshape.PriorityAdmitter {
	return g.priorityAdmitter
}

// GetFaultInjectors returns the fault injection manager.
func (g *Gateway) GetFaultInjectors() *trafficshape.FaultInjectionByRoute {
	return g.faultInjectors
}

// GetRateLimiters returns the rate limiter manager.
func (g *Gateway) GetRateLimiters() *ratelimit.RateLimitByRoute {
	return g.rateLimiters
}

// GetMirrors returns the mirror manager.
func (g *Gateway) GetMirrors() *mirror.MirrorByRoute {
	return g.mirrors
}

// GetGraphQLParsers returns the GraphQL parser manager.
func (g *Gateway) GetGraphQLParsers() *graphql.GraphQLByRoute {
	return g.graphqlParsers
}

// GetCanaryControllers returns the canary controller manager.
func (g *Gateway) GetCanaryControllers() *canary.CanaryByRoute {
	return g.canaryControllers
}

// GetBlueGreenControllers returns the blue-green controller manager.
func (g *Gateway) GetBlueGreenControllers() *bluegreen.BlueGreenByRoute {
	return g.blueGreenControllers
}

// GetABTests returns the A/B test manager.
func (g *Gateway) GetABTests() *abtest.ABTestByRoute {
	return g.abTests
}

// GetTrafficReplay returns the traffic replay manager.
func (g *Gateway) GetTrafficReplay() *trafficreplay.ReplayByRoute {
	return g.trafficReplay
}

// GetRequestQueues returns the request queue manager.
func (g *Gateway) GetRequestQueues() *requestqueue.RequestQueueByRoute {
	return g.requestQueues
}

// GetAdaptiveLimiters returns the adaptive concurrency limiter manager.
func (g *Gateway) GetAdaptiveLimiters() *trafficshape.AdaptiveConcurrencyByRoute {
	return g.adaptiveLimiters
}

// GetErrorPages returns the error pages manager.
func (g *Gateway) GetErrorPages() *errorpages.ErrorPagesByRoute {
	return g.errorPages
}

// GetMaintenanceHandlers returns the maintenance ByRoute manager.
func (g *Gateway) GetMaintenanceHandlers() *maintenance.MaintenanceByRoute {
	return g.maintenanceHandlers
}

// GetHTTPSRedirect returns the HTTPS redirect handler (may be nil).
func (g *Gateway) GetHTTPSRedirect() *httpsredirect.CompiledHTTPSRedirect {
	return g.httpsRedirect
}

// GetAllowedHosts returns the allowed hosts handler (may be nil).
func (g *Gateway) GetAllowedHosts() *allowedhosts.CompiledAllowedHosts {
	return g.allowedHosts
}

// GetTokenChecker returns the token revocation checker (may be nil).
func (g *Gateway) GetTokenChecker() *tokenrevoke.TokenChecker {
	return g.tokenChecker
}

// GetUpstreams returns the configured upstream map.
func (g *Gateway) GetUpstreams() map[string]config.UpstreamConfig {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.config.Upstreams
}

// GetFollowRedirectStats returns per-route redirect transport stats.
func (g *Gateway) GetFollowRedirectStats() map[string]interface{} {
	result := make(map[string]interface{})
	for routeID, rp := range *g.routeProxies.Load() {
		if rt := rp.GetRedirectTransport(); rt != nil {
			result[routeID] = rt.Stats()
		}
	}
	return result
}

// GetTransportPool returns the proxy's transport pool.
func (g *Gateway) GetTransportPool() *proxy.TransportPool {
	return g.proxy.GetTransportPool()
}

// buildTransportPool constructs a TransportPool from the config.
// Three-level merge: defaults → global transport → per-upstream transport.
func (g *Gateway) buildTransportPool(cfg *config.Config) *proxy.TransportPool {
	// Start from defaults, apply global config
	baseCfg := proxy.MergeTransportConfigs(proxy.DefaultTransportConfig, cfg.Transport)

	// Apply DNS resolver if configured
	if len(cfg.DNSResolver.Nameservers) > 0 {
		baseCfg.Resolver = proxy.NewResolver(cfg.DNSResolver.Nameservers, cfg.DNSResolver.Timeout)
	}

	// Apply SSRF protection if configured
	if cfg.SSRFProtection.Enabled {
		baseCfg.SSRFProtection = &cfg.SSRFProtection
	}

	pool := proxy.NewTransportPoolWithDefault(baseCfg)

	// Create per-upstream transports
	for name, us := range cfg.Upstreams {
		if us.Transport == (config.TransportConfig{}) {
			continue // no per-upstream overrides
		}
		usCfg := proxy.MergeTransportConfigs(baseCfg, us.Transport)
		pool.Set(name, usCfg)
	}

	return pool
}

// GetLoadBalancerInfo returns per-route load balancer algorithm and stats.
func (g *Gateway) GetLoadBalancerInfo() map[string]interface{} {
	proxies := *g.routeProxies.Load()
	result := make(map[string]interface{})
	for _, routeCfg := range g.config.Routes {
		info := map[string]interface{}{
			"algorithm": routeCfg.LoadBalancer,
		}
		if info["algorithm"] == "" {
			if len(routeCfg.TrafficSplit) > 0 {
				info["algorithm"] = "weighted_round_robin"
			} else {
				info["algorithm"] = "round_robin"
			}
		}
		if routeCfg.LoadBalancer == "consistent_hash" {
			info["consistent_hash"] = map[string]interface{}{
				"key":         routeCfg.ConsistentHash.Key,
				"header_name": routeCfg.ConsistentHash.HeaderName,
				"replicas":    routeCfg.ConsistentHash.Replicas,
			}
		}
		if routeCfg.LoadBalancer == "least_response_time" {
			if rp, ok := proxies[routeCfg.ID]; ok {
				if lrt, ok := rp.GetBalancer().(*loadbalancer.LeastResponseTime); ok {
					info["latencies"] = lrt.GetLatencies()
				}
			}
		}
		result[routeCfg.ID] = info
	}
	return result
}

// GetTrafficSplitStats returns per-route traffic split information.
func (g *Gateway) GetTrafficSplitStats() map[string]interface{} {
	result := make(map[string]interface{})
	for routeID, rp := range *g.routeProxies.Load() {
		wb, ok := rp.GetBalancer().(*loadbalancer.WeightedBalancer)
		if !ok {
			continue
		}
		groups := wb.GetGroups()
		groupInfos := make([]map[string]interface{}, 0, len(groups))
		for _, g := range groups {
			backends := g.Balancer.GetBackends()
			healthy := 0
			for _, b := range backends {
				if b.Healthy {
					healthy++
				}
			}
			groupInfos = append(groupInfos, map[string]interface{}{
				"name":             g.Name,
				"weight":           g.Weight,
				"backends_total":   len(backends),
				"backends_healthy": healthy,
			})
		}
		info := map[string]interface{}{
			"groups": groupInfos,
			"sticky": wb.HasStickyPolicy(),
		}
		result[routeID] = info
	}
	return result
}

// GetAPIKeyAuth returns the API key auth for admin API
func (g *Gateway) GetAPIKeyAuth() *auth.APIKeyAuth {
	return g.apiKeyAuth
}

// Stats returns gateway statistics
type Stats struct {
	Routes        int            `json:"routes"`
	HealthyRoutes int            `json:"healthy_routes"`
	Backends      map[string]int `json:"backends"`
}

// GetStats returns current gateway statistics
func (g *Gateway) GetStats() *Stats {
	proxies := *g.routeProxies.Load()
	stats := &Stats{
		Routes:   len(proxies),
		Backends: make(map[string]int),
	}

	for routeID, rp := range proxies {
		backends := rp.GetBalancer().GetBackends()
		stats.Backends[routeID] = len(backends)

		healthyCount := 0
		for _, b := range backends {
			if b.Healthy {
				healthyCount++
			}
		}
		if healthyCount > 0 {
			stats.HealthyRoutes++
		}
	}

	return stats
}


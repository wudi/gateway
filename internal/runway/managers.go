package runway

import (
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/abtest"
	"github.com/wudi/runway/internal/bluegreen"
	"github.com/wudi/runway/internal/cache"
	"github.com/wudi/runway/internal/canary"
	"github.com/wudi/runway/internal/circuitbreaker"
	"github.com/wudi/runway/internal/coalesce"
	"github.com/wudi/runway/internal/graphql"
	"github.com/wudi/runway/internal/graphql/federation"
	"github.com/wudi/runway/internal/loadbalancer/outlier"
	"github.com/wudi/runway/internal/middleware/accesslog"
	"github.com/wudi/runway/internal/middleware/ai"
	"github.com/wudi/runway/internal/middleware/aicrawl"
	"github.com/wudi/runway/internal/middleware/auditlog"
	"github.com/wudi/runway/internal/middleware/auth"
	"github.com/wudi/runway/internal/middleware/backendauth"
	"github.com/wudi/runway/internal/middleware/backendenc"
	"github.com/wudi/runway/internal/middleware/backpressure"
	"github.com/wudi/runway/internal/middleware/baggage"
	"github.com/wudi/runway/internal/middleware/bodygen"
	"github.com/wudi/runway/internal/middleware/botdetect"
	"github.com/wudi/runway/internal/middleware/cdnheaders"
	"github.com/wudi/runway/internal/middleware/claimsprop"
	"github.com/wudi/runway/internal/middleware/clientmtls"
	"github.com/wudi/runway/internal/middleware/compression"
	"github.com/wudi/runway/internal/middleware/connect"
	"github.com/wudi/runway/internal/middleware/consumergroup"
	"github.com/wudi/runway/internal/middleware/contentneg"
	"github.com/wudi/runway/internal/middleware/contentreplacer"
	"github.com/wudi/runway/internal/middleware/cors"
	"github.com/wudi/runway/internal/middleware/costtrack"
	"github.com/wudi/runway/internal/middleware/csrf"
	"github.com/wudi/runway/internal/middleware/decompress"
	"github.com/wudi/runway/internal/middleware/dedup"
	"github.com/wudi/runway/internal/middleware/deprecation"
	"github.com/wudi/runway/internal/middleware/edgecacherules"
	"github.com/wudi/runway/internal/middleware/errorhandling"
	"github.com/wudi/runway/internal/middleware/errorpages"
	"github.com/wudi/runway/internal/middleware/etag"
	"github.com/wudi/runway/internal/middleware/extauth"
	"github.com/wudi/runway/internal/middleware/fieldencrypt"
	"github.com/wudi/runway/internal/middleware/fieldreplacer"
	"github.com/wudi/runway/internal/middleware/geo"
	"github.com/wudi/runway/internal/middleware/graphqlsub"
	"github.com/wudi/runway/internal/middleware/idempotency"
	"github.com/wudi/runway/internal/middleware/inboundsigning"
	"github.com/wudi/runway/internal/middleware/ipblocklist"
	"github.com/wudi/runway/internal/middleware/ipfilter"
	"github.com/wudi/runway/internal/middleware/jmespath"
	"github.com/wudi/runway/internal/middleware/luascript"
	"github.com/wudi/runway/internal/middleware/maintenance"
	"github.com/wudi/runway/internal/middleware/mock"
	"github.com/wudi/runway/internal/middleware/modifiers"
	"github.com/wudi/runway/internal/middleware/nonce"
	openapivalidation "github.com/wudi/runway/internal/middleware/openapi"
	"github.com/wudi/runway/internal/middleware/opa"
	"github.com/wudi/runway/internal/middleware/paramforward"
	"github.com/wudi/runway/internal/middleware/piiredact"
	"github.com/wudi/runway/internal/middleware/proxyratelimit"
	"github.com/wudi/runway/internal/middleware/quota"
	"github.com/wudi/runway/internal/middleware/ratelimit"
	"github.com/wudi/runway/internal/middleware/realip"
	"github.com/wudi/runway/internal/middleware/requestqueue"
	"github.com/wudi/runway/internal/middleware/respbodygen"
	"github.com/wudi/runway/internal/middleware/responselimit"
	"github.com/wudi/runway/internal/middleware/responsesigning"
	"github.com/wudi/runway/internal/middleware/securityheaders"
	"github.com/wudi/runway/internal/middleware/signing"
	"github.com/wudi/runway/internal/middleware/slo"
	"github.com/wudi/runway/internal/middleware/spikearrest"
	"github.com/wudi/runway/internal/middleware/sse"
	"github.com/wudi/runway/internal/middleware/staticfiles"
	"github.com/wudi/runway/internal/middleware/statusmap"
	"github.com/wudi/runway/internal/middleware/streaming"
	"github.com/wudi/runway/internal/middleware/tenant"
	"github.com/wudi/runway/internal/middleware/timeout"
	"github.com/wudi/runway/internal/middleware/tokenexchange"
	"github.com/wudi/runway/internal/middleware/tokenrevoke"
	"github.com/wudi/runway/internal/middleware/validation"
	"github.com/wudi/runway/internal/middleware/versioning"
	"github.com/wudi/runway/internal/middleware/waf"
	wasmPlugin "github.com/wudi/runway/internal/middleware/wasm"
	"github.com/wudi/runway/internal/mirror"
	amqpproxy "github.com/wudi/runway/internal/proxy/amqp"
	fastcgiproxy "github.com/wudi/runway/internal/proxy/fastcgi"
	grpcproxy "github.com/wudi/runway/internal/proxy/grpc"
	lambdaproxy "github.com/wudi/runway/internal/proxy/lambda"
	"github.com/wudi/runway/internal/proxy/aggregate"
	"github.com/wudi/runway/internal/proxy/protocol"
	pubsubproxy "github.com/wudi/runway/internal/proxy/pubsub"
	"github.com/wudi/runway/internal/proxy/sequential"
	"github.com/wudi/runway/internal/retry"
	"github.com/wudi/runway/internal/rules"
	"github.com/wudi/runway/internal/trafficreplay"
	"github.com/wudi/runway/internal/trafficshape"
	"github.com/wudi/runway/internal/webhook"
)

// routeManagers holds all per-route and per-config-reload manager objects.
// It is embedded in both Runway and gatewayState so that field access is
// transparent (e.g. g.ipFilters resolves to g.routeManagers.ipFilters).
// On reload, the entire struct is swapped atomically under g.mu.
type routeManagers struct {
	// Auth providers
	apiKeyAuth *auth.APIKeyAuth
	jwtAuth    *auth.JWTAuth
	oauthAuth  *auth.OAuthAuth
	basicAuth  *auth.BasicAuth
	ldapAuth   *auth.LDAPAuth
	samlAuth   *auth.SAMLAuth

	// Per-route managers (ByRoute types)
	rateLimiters      *ratelimit.RateLimitByRoute
	circuitBreakers   *circuitbreaker.BreakerByRoute
	caches            *cache.CacheByRoute
	ipFilters         *ipfilter.IPFilterByRoute
	corsHandlers      *cors.CORSByRoute
	compressors       *compression.CompressorByRoute
	validators        *validation.ValidatorByRoute
	mirrors           *mirror.MirrorByRoute
	grpcHandlers      *grpcproxy.GRPCByRoute
	grpcReflection    *grpcproxy.ReflectionByRoute
	translators       *protocol.TranslatorByRoute
	federationHandlers *federation.FederationByRoute
	routeRules        *rules.RulesByRoute
	throttlers        *trafficshape.ThrottleByRoute
	bandwidthLimiters *trafficshape.BandwidthByRoute
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
	geoFilters        *geo.GeoByRoute
	idempotencyHandlers *idempotency.IdempotencyByRoute
	backendSigners      *signing.SigningByRoute
	decompressors       *decompress.DecompressorByRoute
	responseLimiters    *responselimit.ResponseLimitByRoute
	securityHeaders     *securityheaders.SecurityHeadersByRoute
	maintenanceHandlers *maintenance.MaintenanceByRoute
	botDetectors        *botdetect.BotDetectByRoute
	aiCrawlControllers  *aicrawl.AICrawlByRoute
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
	edgeCacheRules      *edgecacherules.EdgeCacheRulesByRoute
	backendEncoders     *backendenc.EncoderByRoute
	sseHandlers         *sse.SSEByRoute
	inboundVerifiers    *inboundsigning.InboundSigningByRoute
	piiRedactors        *piiredact.PIIRedactByRoute
	fieldEncryptors     *fieldencrypt.FieldEncryptByRoute
	blueGreenControllers *bluegreen.BlueGreenByRoute
	abTests              *abtest.ABTestByRoute
	requestQueues        *requestqueue.RequestQueueByRoute
	dedupHandlers        *dedup.DedupByRoute
	ipBlocklists         *ipblocklist.BlocklistByRoute
	clientMTLSVerifiers  *clientmtls.ClientMTLSByRoute
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
	deprecationHandlers  *deprecation.DeprecationByRoute
	sloTrackers          *slo.SLOByRoute
	etagHandlers         *etag.ETagByRoute
	streamHandlers       *streaming.StreamByRoute
	opaEnforcers         *opa.OPAByRoute
	responseSigners      *responsesigning.SignerByRoute
	costTrackers         *costtrack.CostByRoute
	consumerGroups       *consumergroup.GroupByRoute
	graphqlSubs          *graphqlsub.SubscriptionByRoute
	connectHandlers      *connect.ConnectByRoute
	aiHandlers           *ai.AIByRoute

	// Global-scope objects that change per config reload
	globalIPFilter   *ipfilter.Filter
	globalBlocklist  *ipblocklist.Blocklist
	globalGeo        *geo.CompiledGeo
	geoProvider      geo.Provider
	globalRules      *rules.RuleEngine
	priorityAdmitter *trafficshape.PriorityAdmitter
	tokenChecker     *tokenrevoke.TokenChecker
	realIPExtractor  *realip.CompiledRealIP
	tenantManager    *tenant.Manager
	budgetPools      map[string]*retry.Budget
	consumerGroupMgr bool // tracks if consumer group manager was set
}

// newRouteManagers creates a fresh set of all per-route managers.
func newRouteManagers(cfg *config.Config, redisClient *redis.Client) routeManagers {
	return routeManagers{
		rateLimiters:      ratelimit.NewRateLimitByRoute(),
		circuitBreakers:   circuitbreaker.NewBreakerByRoute(),
		caches:            cache.NewCacheByRoute(redisClient),
		ipFilters:         ipfilter.NewIPFilterByRoute(),
		corsHandlers:      cors.NewCORSByRoute(),
		compressors:       compression.NewCompressorByRoute(),
		validators:        validation.NewValidatorByRoute(),
		mirrors:           mirror.NewMirrorByRoute(),
		grpcHandlers:      grpcproxy.NewGRPCByRoute(),
		grpcReflection:    grpcproxy.NewReflectionByRoute(),
		translators:       protocol.NewTranslatorByRoute(),
		federationHandlers: federation.NewFederationByRoute(),
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
		nonceCheckers:     nonce.NewNonceByRoute(redisClient),
		csrfProtectors:    csrf.NewCSRFByRoute(),
		outlierDetectors:  outlier.NewDetectorByRoute(),
		geoFilters:        geo.NewGeoByRoute(),
		idempotencyHandlers: idempotency.NewIdempotencyByRoute(redisClient),
		backendSigners:      signing.NewSigningByRoute(),
		decompressors:       decompress.NewDecompressorByRoute(),
		responseLimiters:    responselimit.NewResponseLimitByRoute(),
		securityHeaders:     securityheaders.NewSecurityHeadersByRoute(),
		maintenanceHandlers: maintenance.NewMaintenanceByRoute(),
		botDetectors:        botdetect.NewBotDetectByRoute(),
		aiCrawlControllers:  aicrawl.NewAICrawlByRoute(),
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
		quotaEnforcers:      quota.NewQuotaByRoute(redisClient),
		sequentialHandlers:  sequential.NewSequentialByRoute(),
		aggregateHandlers:   aggregate.NewAggregateByRoute(),
		respBodyGenerators:  respbodygen.NewRespBodyGenByRoute(),
		paramForwarders:     paramforward.NewParamForwardByRoute(),
		contentNegotiators:  contentneg.NewNegotiatorByRoute(),
		cdnHeaders:          cdnheaders.NewCDNHeadersByRoute(),
		edgeCacheRules:      edgecacherules.NewEdgeCacheRulesByRoute(),
		backendEncoders:     backendenc.NewEncoderByRoute(),
		sseHandlers:         sse.NewSSEByRoute(),
		inboundVerifiers:    inboundsigning.NewInboundSigningByRoute(),
		piiRedactors:        piiredact.NewPIIRedactByRoute(),
		fieldEncryptors:     fieldencrypt.NewFieldEncryptByRoute(),
		blueGreenControllers: bluegreen.NewBlueGreenByRoute(),
		abTests:              abtest.NewABTestByRoute(),
		requestQueues:        requestqueue.NewRequestQueueByRoute(),
		dedupHandlers:        dedup.NewDedupByRoute(redisClient),
		ipBlocklists:         ipblocklist.NewBlocklistByRoute(),
		clientMTLSVerifiers:  clientmtls.NewClientMTLSByRoute(),
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
		deprecationHandlers:  deprecation.NewDeprecationByRoute(),
		sloTrackers:          slo.NewSLOByRoute(),
		etagHandlers:         etag.NewETagByRoute(),
		streamHandlers:       streaming.NewStreamByRoute(),
		opaEnforcers:         opa.NewOPAByRoute(),
		responseSigners:      responsesigning.NewSignerByRoute(),
		costTrackers:         costtrack.NewCostByRoute(),
		consumerGroups:       consumergroup.NewGroupByRoute(),
		graphqlSubs:          graphqlsub.NewSubscriptionByRoute(),
		connectHandlers:      connect.NewConnectByRoute(),
		aiHandlers:           ai.NewAIByRoute(),
		budgetPools:          make(map[string]*retry.Budget),
	}
}

// initGlobals initializes the global singletons on routeManagers from config.
// This is shared by both New() and buildState() to prevent divergence.
func (rm *routeManagers) initGlobals(cfg *config.Config, redisClient *redis.Client) error {
	// Retry budget pools
	for name, bc := range cfg.RetryBudgets {
		rm.budgetPools[name] = retry.NewBudget(bc.Ratio, bc.MinRetries, bc.Window)
	}

	// Priority admitter
	if cfg.TrafficShaping.Priority.Enabled {
		rm.priorityAdmitter = trafficshape.NewPriorityAdmitter(cfg.TrafficShaping.Priority.MaxConcurrent)
	}

	// Consumer groups
	if cfg.ConsumerGroups.Enabled {
		rm.consumerGroups.SetManager(consumergroup.NewGroupManager(cfg.ConsumerGroups))
	}

	// Tenant manager
	if cfg.Tenants.Enabled {
		rm.tenantManager = tenant.NewManager(cfg.Tenants, redisClient)
	}

	// Global IP filter
	if cfg.IPFilter.Enabled {
		var err error
		rm.globalIPFilter, err = ipfilter.New(cfg.IPFilter)
		if err != nil {
			return fmt.Errorf("failed to initialize global IP filter: %w", err)
		}
	}

	// Global IP blocklist
	if cfg.IPBlocklist.Enabled {
		var err error
		rm.globalBlocklist, err = ipblocklist.New(cfg.IPBlocklist)
		if err != nil {
			return fmt.Errorf("failed to initialize global IP blocklist: %w", err)
		}
	}

	// Geo provider + global geo filter
	if cfg.Geo.Enabled && cfg.Geo.Database != "" {
		var err error
		rm.geoProvider, err = geo.NewProvider(cfg.Geo.Database)
		if err != nil {
			return fmt.Errorf("failed to initialize geo provider: %w", err)
		}
		rm.geoFilters.SetProvider(rm.geoProvider)
		rm.globalGeo, err = geo.New("_global", cfg.Geo, rm.geoProvider)
		if err != nil {
			return fmt.Errorf("failed to initialize global geo filter: %w", err)
		}
	}

	// Trusted proxies / real IP extractor
	if len(cfg.TrustedProxies.CIDRs) > 0 {
		var err error
		rm.realIPExtractor, err = realip.New(cfg.TrustedProxies.CIDRs, cfg.TrustedProxies.Headers, cfg.TrustedProxies.MaxHops)
		if err != nil {
			return fmt.Errorf("failed to initialize trusted proxies: %w", err)
		}
	}

	// Global rules engine
	if len(cfg.Rules.Request) > 0 || len(cfg.Rules.Response) > 0 {
		var err error
		rm.globalRules, err = rules.NewEngine(cfg.Rules.Request, cfg.Rules.Response)
		if err != nil {
			return fmt.Errorf("failed to compile global rules: %w", err)
		}
	}

	// Token revocation checker
	if cfg.TokenRevocation.Enabled {
		rm.tokenChecker = tokenrevoke.New(cfg.TokenRevocation, redisClient)
	}

	return nil
}

// initAuth initializes all authentication providers from config.
// This is shared by both New() and buildState() to prevent divergence.
func (rm *routeManagers) initAuth(cfg *config.Config) error {
	if cfg.Authentication.APIKey.Enabled {
		rm.apiKeyAuth = auth.NewAPIKeyAuth(cfg.Authentication.APIKey)

		if cfg.Authentication.APIKey.Management.Enabled {
			mgmt := cfg.Authentication.APIKey.Management
			store := auth.NewMemoryKeyStore(60 * time.Second)

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
			rm.apiKeyAuth.SetManager(manager)
		}
	}

	if cfg.Authentication.JWT.Enabled {
		var err error
		rm.jwtAuth, err = auth.NewJWTAuth(cfg.Authentication.JWT)
		if err != nil {
			return fmt.Errorf("failed to initialize JWT auth: %w", err)
		}
	}

	if cfg.Authentication.OAuth.Enabled {
		var err error
		rm.oauthAuth, err = auth.NewOAuthAuth(cfg.Authentication.OAuth)
		if err != nil {
			return fmt.Errorf("failed to initialize OAuth auth: %w", err)
		}
	}

	if cfg.Authentication.Basic.Enabled {
		rm.basicAuth = auth.NewBasicAuth(cfg.Authentication.Basic)
	}

	if cfg.Authentication.LDAP.Enabled {
		var err error
		rm.ldapAuth, err = auth.NewLDAPAuth(cfg.Authentication.LDAP)
		if err != nil {
			return fmt.Errorf("failed to initialize LDAP auth: %w", err)
		}
	}

	if cfg.Authentication.SAML.Enabled {
		var err error
		rm.samlAuth, err = auth.NewSAMLAuth(cfg.Authentication.SAML)
		if err != nil {
			return fmt.Errorf("failed to initialize SAML auth: %w", err)
		}
	}

	return nil
}

// cleanup releases resources held by routeManagers. Called on old state after a reload swap.
func (rm *routeManagers) cleanup() {
	rm.translators.Close()
	rm.extAuths.CloseAll()
	rm.canaryControllers.StopAll()
	rm.blueGreenControllers.StopAll()
	rm.adaptiveLimiters.CloseAll()
	rm.nonceCheckers.CloseAll()
	rm.outlierDetectors.StopAll()
	rm.idempotencyHandlers.CloseAll()
	rm.quotaEnforcers.CloseAll()
	rm.backpressureHandlers.CloseAll()
	rm.auditLoggers.CloseAll()
	rm.dedupHandlers.CloseAll()
	rm.sseHandlers.CloseAll()
	rm.ipBlocklists.CloseAll()
	if rm.tenantManager != nil {
		rm.tenantManager.Close()
	}
	if rm.tokenChecker != nil {
		rm.tokenChecker.Close()
	}
	if rm.jwtAuth != nil {
		rm.jwtAuth.Close()
	}
	if rm.ldapAuth != nil {
		rm.ldapAuth.Close()
	}
	if rm.samlAuth != nil {
		rm.samlAuth.Close()
	}
}

// wireWebhookCallbacks sets up event callbacks on circuit breakers, canary controllers,
// and outlier detectors to emit webhook events. This is shared by New() and buildState().
func (rm *routeManagers) wireWebhookCallbacks(dispatcher *webhook.Dispatcher) {
	if dispatcher == nil {
		return
	}
	rm.circuitBreakers.SetOnStateChange(func(routeID, from, to string) {
		dispatcher.Emit(webhook.NewEvent(webhook.CircuitBreakerStateChange, routeID, map[string]interface{}{
			"from": from, "to": to,
		}))
	})
	rm.canaryControllers.SetOnEvent(func(routeID, eventType string, data map[string]interface{}) {
		dispatcher.Emit(webhook.NewEvent(webhook.EventType(eventType), routeID, data))
	})
	rm.outlierDetectors.SetCallbacks(
		func(routeID, backend, reason string) {
			dispatcher.Emit(webhook.NewEvent(webhook.OutlierEjected, routeID, map[string]interface{}{
				"backend": backend, "reason": reason,
			}))
		},
		func(routeID, backend string) {
			dispatcher.Emit(webhook.NewEvent(webhook.OutlierRecovered, routeID, map[string]interface{}{
				"backend": backend,
			}))
		},
	)
}


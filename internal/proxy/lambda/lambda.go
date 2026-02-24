package lambda

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/variables"
)

// Handler invokes AWS Lambda functions as backend.
type Handler struct {
	client       *awslambda.Client
	functionName string
	maxRetries   int

	totalRequests  atomic.Int64
	totalErrors    atomic.Int64
	totalInvokes   atomic.Int64
}

// New creates a Lambda handler from config.
func New(cfg config.LambdaConfig) (*Handler, error) {
	if cfg.FunctionName == "" {
		return nil, fmt.Errorf("lambda: function_name is required")
	}

	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
	)
	if err != nil {
		return nil, fmt.Errorf("lambda: failed to load AWS config: %w", err)
	}

	client := awslambda.NewFromConfig(awsCfg)

	return &Handler{
		client:       client,
		functionName: cfg.FunctionName,
		maxRetries:   cfg.MaxRetries,
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.totalRequests.Add(1)

	varCtx := variables.GetFromRequest(r)

	// Build payload based on method
	var payload interface{}
	switch r.Method {
	case http.MethodGet:
		p := map[string]interface{}{
			"httpMethod":      r.Method,
			"path":            r.URL.Path,
			"queryParameters": r.URL.Query(),
			"headers":         flattenHeaders(r.Header),
		}
		if varCtx != nil {
			p["pathParameters"] = varCtx.PathParams
		}
		payload = p
	default:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			h.totalErrors.Add(1)
			http.Error(w, "lambda: read body failed", http.StatusBadGateway)
			return
		}
		p := map[string]interface{}{
			"httpMethod": r.Method,
			"path":       r.URL.Path,
			"headers":    flattenHeaders(r.Header),
			"body":       string(body),
		}
		if varCtx != nil {
			p["pathParameters"] = varCtx.PathParams
		}
		payload = p
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "lambda: marshal payload failed", http.StatusBadGateway)
		return
	}

	h.totalInvokes.Add(1)
	result, err := h.client.Invoke(r.Context(), &awslambda.InvokeInput{
		FunctionName: aws.String(h.functionName),
		Payload:      payloadBytes,
	})
	if err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "lambda: invoke failed", http.StatusBadGateway)
		return
	}

	if result.FunctionError != nil {
		h.totalErrors.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write(result.Payload)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(result.Payload)
}

func flattenHeaders(h http.Header) map[string]string {
	result := make(map[string]string, len(h))
	for k := range h {
		result[k] = h.Get(k)
	}
	return result
}

// Stats returns handler stats.
func (h *Handler) Stats() map[string]interface{} {
	return map[string]interface{}{
		"function_name":  h.functionName,
		"total_requests": h.totalRequests.Load(),
		"total_errors":   h.totalErrors.Load(),
		"total_invokes":  h.totalInvokes.Load(),
	}
}

// LambdaByRoute manages per-route Lambda handlers.
type LambdaByRoute struct {
	byroute.Manager[*Handler]
}

func NewLambdaByRoute() *LambdaByRoute {
	return &LambdaByRoute{}
}

func (m *LambdaByRoute) AddRoute(routeID string, cfg config.LambdaConfig) error {
	h, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, h)
	return nil
}

func (m *LambdaByRoute) GetHandler(routeID string) *Handler {
	v, _ := m.Get(routeID)
	return v
}

func (m *LambdaByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(h *Handler) interface{} { return h.Stats() })
}

// This is a compile-time verification that the public gateway API is importable
// and the builder pattern works. It is NOT meant to be run.
package main

import (
	"fmt"
	"net/http"
	"os"

	gw "github.com/wudi/gateway/gateway"
)

func main() {
	cfg, err := gw.LoadConfig("gateway.yaml")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	server, err := gw.New(cfg).
		WithDefaults().
		AddMiddleware(gw.MiddlewareSlot{
			Name:  "custom_check",
			After: gw.MWAuth,
			Build: func(routeID string, cfg gw.RouteConfig) gw.Middleware {
				return func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						// Custom middleware logic
						next.ServeHTTP(w, r)
					})
				}
			},
		}).
		AddGlobalMiddleware(gw.GlobalMiddlewareSlot{
			Name:  "custom_global",
			After: gw.MWGlobalRequestID,
			Build: func(cfg *gw.Config) gw.Middleware {
				return func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						next.ServeHTTP(w, r)
					})
				}
			},
		}).
		Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Verify Server methods compile
	_ = server.Handler()
	_ = server.IsDraining()
	server.Drain()

	// Verify extension helpers compile
	if gw.HasExtension(cfg.Extensions, "my_plugin") {
		type MyConfig struct {
			Enabled bool `yaml:"enabled"`
		}
		mc, err := gw.ParseExtension[MyConfig](cfg.Extensions, "my_plugin")
		_ = mc
		_ = err
	}

	server.Run()
}

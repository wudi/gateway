// +build ignore

// Mock backend server for testing the runway
// Run with: go run scripts/mock-backend.go -port 9001
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"
)

func main() {
	port := flag.Int("port", 9001, "Port to listen on")
	name := flag.String("name", "backend", "Backend name")
	flag.Parse()

	mux := http.NewServeMux()

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"backend": *name,
		})
	})

	// Echo endpoint - returns request info
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		response := map[string]interface{}{
			"backend":    *name,
			"path":       r.URL.Path,
			"method":     r.Method,
			"query":      r.URL.RawQuery,
			"host":       r.Host,
			"remote_addr": r.RemoteAddr,
			"timestamp":  time.Now().Format(time.RFC3339),
			"headers":    headerMap(r.Header),
		}

		json.NewEncoder(w).Encode(response)
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Mock backend '%s' starting on %s", *name, addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func headerMap(h http.Header) map[string]string {
	result := make(map[string]string)
	for k, v := range h {
		if len(v) > 0 {
			result[k] = v[0]
		}
	}
	return result
}

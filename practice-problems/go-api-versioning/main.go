// Package main is a tiny shop API. The mobile app calls /orders and /users/{id};
// only the load balancer calls /healthz.
package main

import (
	"fmt"
	"net/http"
)

func routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") })
	mux.HandleFunc("GET /orders", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `[{"id":"o1"}]`) })
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"id":%q}`, r.PathValue("id")) })
	return mux
}

func main() { _ = http.ListenAndServe("127.0.0.1:8080", routes()) }

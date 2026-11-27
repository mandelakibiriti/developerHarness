// The goal test: do not edit it, change main.go until it passes.
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoutes(t *testing.T) {
	for path, want := range map[string]int{
		"/v1/orders":   http.StatusOK, // called from outside: versioned
		"/v1/users/42": http.StatusOK,
		"/healthz":     http.StatusOK, // operational, not part of the public API
		"/v1/healthz":  http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHealthResponse checks both routes across readiness changes without altering failures.
func TestHealthResponse(t *testing.T) {
	s, err := NewServer(Config{BindAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s.server.Handler)
	defer server.Close()
	for _, ready := range []bool{false, true, false} {
		if ready {
			s.SetReady()
		} else {
			s.SetNotReady()
		}
		wantStatus, wantBody := http.StatusServiceUnavailable, "Service Unavailable\n"
		if ready {
			wantStatus, wantBody = http.StatusOK, "ok"
		}
		for _, path := range []string{"/health", "/"} {
			response, err := server.Client().Get(server.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != wantStatus || string(body) != wantBody || response.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
				t.Fatalf("ready=%t path=%s: status=%d body=%q content-type=%q", ready, path, response.StatusCode, body, response.Header.Get("Content-Type"))
			}
		}
	}
}

// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHealthResponse checks the exact successful probe response over HTTP.
func TestHealthResponse(t *testing.T) {
	s, err := New(Config{BindAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s.httpServer.Handler)
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "ok" || response.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("status=%d body=%q content-type=%q", response.StatusCode, body, response.Header.Get("Content-Type"))
	}
}

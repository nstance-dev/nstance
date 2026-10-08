// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestServe checks real IPv4/IPv6 requests, route isolation, and listener shutdown.
func TestServe(t *testing.T) {
	previousMux := http.DefaultServeMux
	http.DefaultServeMux = http.NewServeMux()
	http.DefaultServeMux.HandleFunc("/debug/healthcheck-test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	t.Cleanup(func() { http.DefaultServeMux = previousMux })
	for _, tc := range []struct{ network, addr string }{
		{"tcp4", "127.0.0.1:0"}, {"tcp6", "[::1]:0"},
	} {
		t.Run(tc.network, func(t *testing.T) {
			listener, err := net.Listen(tc.network, tc.addr)
			if err != nil {
				if tc.network == "tcp6" {
					t.Skipf("IPv6 loopback unavailable: %v", err)
				}
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(func() { cancel(); _ = listener.Close() })
			done := make(chan error, 1)
			go func() { done <- Serve(ctx, listener) }()
			client := &http.Client{Transport: &http.Transport{}, Timeout: 3 * time.Second}
			defer client.CloseIdleConnections()
			for _, req := range []struct {
				method, path string
				status       int
			}{
				{http.MethodGet, "/healthz", http.StatusOK},
				{http.MethodHead, "/healthz", http.StatusOK},
				{http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
				{http.MethodGet, "/healthz/", http.StatusNotFound},
				{http.MethodGet, "/", http.StatusNotFound},
				{http.MethodGet, "/metrics", http.StatusNotFound},
				{http.MethodGet, "/debug/healthcheck-test", http.StatusNotFound},
			} {
				request, err := http.NewRequest(req.method, "http://"+listener.Addr().String()+req.path, nil)
				if err != nil {
					t.Fatal(err)
				}
				response, err := client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != req.status {
					t.Fatalf("%s %s: got %d, want %d", req.method, req.path, response.StatusCode, req.status)
				}
				if req.status == http.StatusOK {
					want := "OK\n"
					if req.method == http.MethodHead {
						want = ""
					}
					if string(body) != want || response.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
						t.Fatalf("%s %s: body=%q content-type=%q", req.method, req.path, body, response.Header.Get("Content-Type"))
					}
				}
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("health server did not stop on cancellation")
			}
			conn, err := net.DialTimeout(tc.network, listener.Addr().String(), time.Second)
			if err == nil {
				_ = conn.Close()
				t.Fatal("health listener still accepts connections after shutdown")
			}
		})
	}
}

// TestServeListenerError ensures unexpected serving failures are not hidden as shutdown.
func TestServeListenerError(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Serve(context.Background(), listener); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("expected closed-listener error, got %v", err)
	}
}

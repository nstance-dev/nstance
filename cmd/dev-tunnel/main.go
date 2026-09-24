// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"log"
	"net/http"
)

// main for dev-tunnel serves a minimal readiness endpoint for local tunnel development.
func main() {
	listen := flag.String("listen", "127.0.0.1:28080", "HTTP readiness listen address")
	flag.Parse()
	server := &http.Server{Addr: *listen, Handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

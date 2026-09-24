// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// acceptLoop accepts connections for one proxy port until the server stops.
func (s *Server) acceptLoop(ctx context.Context, listeners *portListeners) {
	defer s.acceptWG.Done()
	for {
		conn, err := listeners.listener.Accept()
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				s.logger.Error("Proxy accept failed", "error", err)
			}
			return
		}
		s.trackConnection(conn)
		s.handlerWG.Add(1)
		go func() {
			defer s.handlerWG.Done()
			s.handleConnection(ctx, conn, listeners)
		}()
	}
}

// handleConnection waits for payload, wakes the listener, and relays the connection.
func (s *Server) handleConnection(serverCtx context.Context, client net.Conn, listeners *portListeners) {
	defer func() {
		s.untrackConnection(client)
		_ = client.Close()
	}()
	deadline := time.Now().Add(s.holdTimeout)
	if err := client.SetReadDeadline(deadline); err != nil {
		return
	}
	firstPayload := make([]byte, 32*1024)
	n, err := client.Read(firstPayload)
	if err != nil {
		// Provider TCP health checks connect without payload and must never wake.
		var netErr net.Error
		if !errors.Is(err, io.EOF) && (!errors.As(err, &netErr) || !netErr.Timeout()) {
			s.logger.Debug("Proxy connection closed before payload", "error", err)
		}
		return
	}
	if n == 0 {
		return
	}
	listeners.mu.RLock()
	routes := listeners.routes
	listeners.mu.RUnlock()
	route := routes.exclusive
	if route == nil {
		tcpAddress, ok := client.LocalAddr().(*net.TCPAddr)
		if !ok {
			return
		}
		destination := tcpAddress.IP.String()
		route = routes.byIP[destination]
		if route == nil {
			s.logger.Warn("No proxy listener for destination", "destination", destination)
			return
		}
	}
	if s.routeSelected != nil {
		s.routeSelected()
	}
	wakeCtx, cancel := context.WithDeadline(serverCtx, deadline)
	upstreamAddress, err := s.waker.Wake(wakeCtx, route.identity)
	cancel()
	if err != nil {
		s.logger.Warn("Listener wake failed", "listener", route.identity, "error", err)
		return
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return
	}
	dialer := net.Dialer{Timeout: min(s.dialTimeout, remaining)}
	upstream, err := dialer.DialContext(serverCtx, "tcp", upstreamAddress)
	if err != nil {
		s.logger.Warn("Ready upstream dial failed", "listener", route.identity, "upstream", upstreamAddress, "error", err)
		return
	}
	s.trackConnection(upstream)
	defer func() {
		s.untrackConnection(upstream)
		_ = upstream.Close()
	}()
	_ = client.SetReadDeadline(time.Time{})
	if _, err := upstream.Write(firstPayload[:n]); err != nil {
		return
	}
	proxyBothDirections(client, upstream)
}

// proxyBothDirections relays both halves of a TCP connection through EOF.
func proxyBothDirections(client, upstream net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	copyOneWay := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if closer, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
	}
	go copyOneWay(upstream, client)
	go copyOneWay(client, upstream)
	wg.Wait()
}

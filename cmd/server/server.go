package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

const shutdownTimeout = 30 * time.Second

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		// Body and response deadlines would interrupt large streaming transfers.
	}
}

func serveHTTP(ctx context.Context, server *http.Server, grace time.Duration) error {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	return serveListener(ctx, server, listener, grace)
}

func serveListener(ctx context.Context, server *http.Server, listener net.Listener, grace time.Duration) error {
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		err := server.Shutdown(shutdownCtx)
		if err != nil {
			// Force remaining connections closed after the grace period.
			_ = server.Close()
		}
		<-result
		return err
	}
}

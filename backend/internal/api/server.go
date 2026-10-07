package api

import (
	"net/http"
	"time"
)

// Timeouts are the http.Server limits.
type Timeouts struct {
	ReadHeader time.Duration
	Read       time.Duration
	Write      time.Duration
	Idle       time.Duration
}

// NewHTTPServer builds the http.Server with slowloris/idle-connection
// protections. WriteTimeout applies to REST responses only: net/http clears
// the connection deadlines when a handler hijacks the connection, so
// long-lived WebSockets are not affected.
func NewHTTPServer(addr string, h http.Handler, t Timeouts) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: t.ReadHeader,
		ReadTimeout:       t.Read,
		WriteTimeout:      t.Write,
		IdleTimeout:       t.Idle,
		MaxHeaderBytes:    1 << 16,
	}
}

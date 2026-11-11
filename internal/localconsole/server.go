// Package localconsole serves the read-only console on IPv4 loopback.
package localconsole

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"git-mcp/internal/audit"
	"git-mcp/internal/console"
)

// Reader is the part of the audit store the console needs.
type Reader interface {
	List(ctx context.Context, limit int) ([]audit.Event, error)
	Search(ctx context.Context, filter audit.Filter, offset, limit int) (audit.Found, error)
}

// Server is a running console. URL is its overview page.
type Server struct {
	URL    string
	server *http.Server
}

// Start binds 127.0.0.1 on a port chosen by the operating system.
// report receives an error that stops the listener after Start returned.
func Start(reader Reader, report func(error)) (*Server, error) {
	if reader == nil {
		return nil, errors.New("leitor de auditoria ausente")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	host := listener.Addr().String()
	server := &http.Server{
		Handler:           newHandler(reader, host),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && report != nil {
			report(err)
		}
	}()
	return &Server{URL: "http://" + host + "/web/overview", server: server}, nil
}

// Close stops the listener and drops open connections.
func (s *Server) Close() error {
	return s.server.Close()
}

// newHandler answers only GET from a loopback peer addressed to host.
// The Host, Origin and Sec-Fetch-Site checks keep another site's page, or a
// DNS name rebound to 127.0.0.1, from reading the console through the browser.
func newHandler(reader Reader, host string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Cache-Control", "no-store")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")

		peerHost, _, err := net.SplitHostPort(r.RemoteAddr)
		peer := net.ParseIP(peerHost)
		origin := r.Header.Get("Origin")
		fetchSite := r.Header.Get("Sec-Fetch-Site")
		switch {
		case err != nil || peer == nil || !peer.IsLoopback(),
			r.Host != host,
			origin != "" && origin != "http://"+host,
			fetchSite != "" && fetchSite != "none" && fetchSite != "same-origin":
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			header.Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var section string
		switch r.URL.Path {
		case "/", "/web", "/web/":
			http.Redirect(w, r, "/web/overview", http.StatusFound)
			return
		case "/web/overview":
			section = console.SectionOverview
		case "/web/monitoring":
			section = console.SectionMonitoring
		case "/web/audit":
			section = console.SectionAudit
		default:
			http.NotFound(w, r)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		now := time.Now()
		var page console.Page
		if section == console.SectionAudit {
			query := console.ParseQuery(r.URL.Query())
			found, err := reader.Search(ctx, query.Filter(now), query.Offset(), console.PageSize)
			page = console.NewAuditPage(query, found, err != nil, now)
		} else {
			events, err := reader.List(ctx, audit.MaxList)
			page = console.NewPage(section, events, err != nil, now)
		}
		var body bytes.Buffer
		if err := console.Render(&body, page); err != nil {
			http.Error(w, "page unavailable", http.StatusInternalServerError)
			return
		}
		header.Set("Content-Type", "text/html; charset=utf-8")
		_, _ = body.WriteTo(w)
	})
}

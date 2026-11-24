package web

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// withHostAllowlist rejects requests whose Host header is not in the allowlist,
// mitigating DNS rebinding against a localhost-bound admin server.
func (s *Server) withHostAllowlist(next http.Handler) http.Handler {
	allowed := buildAllowedHosts(s.cfg.Listen, s.cfg.AllowedHosts)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := normalizeHost(r.Host)
		if host == "" {
			http.Error(w, "Forbidden: missing host", http.StatusForbidden)
			return
		}
		if _, ok := allowed[host]; !ok {
			http.Error(w, "Forbidden: host not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func buildAllowedHosts(listen string, extra []string) map[string]struct{} {
	allowed := map[string]struct{}{
		"localhost": {},
		"127.0.0.1": {},
		"::1":       {},
	}

	if listenHost := normalizeHost(listen); listenHost != "" && listenHost != "0.0.0.0" && listenHost != "::" {
		allowed[listenHost] = struct{}{}
	}

	for _, entry := range extra {
		if h := normalizeHost(entry); h != "" {
			allowed[h] = struct{}{}
		}
	}

	return allowed
}

// normalizeHost extracts the lowercase hostname without port or IPv6 brackets.
func normalizeHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(raw, "]")
	return strings.ToLower(strings.TrimSpace(raw))
}

// withCSRFGuard rejects unsafe HTTP methods unless Sec-Fetch-Site is
// same-origin/none, or (when absent) Origin/Referer matches the request host.
func (s *Server) withCSRFGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		if sfs := strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")); sfs != "" {
			switch strings.ToLower(sfs) {
			case "same-origin", "none":
				next.ServeHTTP(w, r)
				return
			default:
				http.Error(w, "Forbidden: cross-site request blocked", http.StatusForbidden)
				return
			}
		}

		source := strings.TrimSpace(r.Header.Get("Origin"))
		if source == "" {
			source = strings.TrimSpace(r.Header.Get("Referer"))
		}
		if source == "" {
			http.Error(w, "Forbidden: missing origin", http.StatusForbidden)
			return
		}

		parsed, err := url.Parse(source)
		if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Host, r.Host) {
			http.Error(w, "Forbidden: origin mismatch", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

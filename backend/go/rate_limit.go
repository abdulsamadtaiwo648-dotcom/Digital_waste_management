package main

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type authLimiter struct {
	mu        sync.Mutex
	attempts  map[string][]time.Time
	lastClean time.Time
}

func newAuthLimiter() *authLimiter {
	return &authLimiter{attempts: make(map[string][]time.Time)}
}

func (l *authLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	const window = 15 * time.Minute
	cutoff := now.Add(-window)
	if now.Sub(l.lastClean) > window {
		for ip, hits := range l.attempts {
			if len(hits) == 0 || hits[len(hits)-1].Before(cutoff) {
				delete(l.attempts, ip)
			}
		}
		l.lastClean = now
	}
	hits := l.attempts[key]
	active := hits[:0]
	for _, hit := range hits {
		if hit.After(cutoff) {
			active = append(active, hit)
		}
	}
	if len(active) >= 10 {
		l.attempts[key] = active
		return false
	}
	l.attempts[key] = append(active, now)
	return true
}

func (s *server) limitAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if !s.authLimiter.allow(ip, time.Now()) {
			w.Header().Set("Retry-After", "900")
			writeError(w, http.StatusTooManyRequests, "too_many_attempts", "Too many account requests. Try again in 15 minutes.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

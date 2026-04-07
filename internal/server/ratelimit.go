package server

import (
	"net/http"
	"sync"
	"time"
)

// Simple per-IP token bucket rate limiter.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    int           // tokens per interval
	interval time.Duration
}

type bucket struct {
	tokens   int
	lastFill time.Time
}

func newRateLimiter(rate int, interval time.Duration) *rateLimiter {
	rl := &rateLimiter{
		buckets:  make(map[string]*bucket),
		rate:     rate,
		interval: interval,
	}
	// Cleanup stale entries every 5 minutes
	go func() {
		for {
			time.Sleep(5 * time.Minute)
			rl.mu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			for ip, b := range rl.buckets {
				if b.lastFill.Before(cutoff) {
					delete(rl.buckets, ip)
				}
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[ip]
	if !ok {
		b = &bucket{tokens: rl.rate, lastFill: time.Now()}
		rl.buckets[ip] = b
	}

	// Refill tokens based on elapsed time
	elapsed := time.Since(b.lastFill)
	if elapsed >= rl.interval {
		refill := int(elapsed / rl.interval) * rl.rate
		b.tokens += refill
		if b.tokens > rl.rate*2 { // cap at 2x burst
			b.tokens = rl.rate * 2
		}
		b.lastFill = time.Now()
	}

	if b.tokens > 0 {
		b.tokens--
		return true
	}
	return false
}

// Middleware wraps a handler with rate limiting.
// clientIP is defined in handlers.go
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !rl.allow(ip) {
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

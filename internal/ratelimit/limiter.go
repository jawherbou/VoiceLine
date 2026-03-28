// Package ratelimit provides per-IP and per-user rate limiting middleware
// using a token bucket algorithm (golang.org/x/time/rate).
//
// Two limiters are stacked:
//   - IP limiter   — guards against unauthenticated abuse
//   - User limiter — controls per-user LLM spend
//
// Both use an in-memory map with a background cleanup goroutine
// that evicts stale entries so memory stays bounded.
package ratelimit

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// Config holds the token bucket parameters for one limiter tier.
type Config struct {
	RequestsPerSecond rate.Limit
	Burst             int
}

// DefaultIPConfig limits unknown/unauthenticated callers conservatively.
var DefaultIPConfig = Config{
	RequestsPerSecond: 2,
	Burst:             10,
}

// DefaultUserConfig is tuned for authenticated users uploading voice memos.
// Audio processing is expensive — 1 request per 2 seconds is generous enough.
var DefaultUserConfig = Config{
	RequestsPerSecond: 0.5,
	Burst:             5,
}

// internal limiter store

type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type store struct {
	mu      sync.Mutex
	entries map[string]*entry
	config  Config
}

func newStore(cfg Config) *store {
	s := &store{
		entries: make(map[string]*entry),
		config:  cfg,
	}
	// Background goroutine prevents unbounded memory growth.
	go s.cleanup()
	return s
}

// get returns the limiter for key, creating one on first access.
func (s *store) get(key string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[key]
	if !ok {
		e = &entry{limiter: rate.NewLimiter(s.config.RequestsPerSecond, s.config.Burst)}
		s.entries[key] = e
	}
	e.lastSeen = time.Now()
	return e.limiter
}

// cleanup evicts entries idle for more than 5 minutes, running every minute.
func (s *store) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		for key, e := range s.entries {
			if time.Since(e.lastSeen) > 5*time.Minute {
				delete(s.entries, key)
			}
		}
		s.mu.Unlock()
	}
}

// Middleware

// RateLimiter holds both limiter stores and exposes a Gin middleware.
type RateLimiter struct {
	ipStore   *store
	userStore *store
}

// New creates a RateLimiter with the provided configs.
func New(ipCfg, userCfg Config) *RateLimiter {
	return &RateLimiter{
		ipStore:   newStore(ipCfg),
		userStore: newStore(userCfg),
	}
}

// Middleware returns a Gin handler that enforces both limiters in sequence.
// IP limiting runs first. User limiting only applies when "user_id" is present
// in the Gin context — meaning an upstream auth middleware ran successfully.
func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Layer 1 — IP limit (catches unauthenticated abuse)
		if !rl.ipStore.get(c.ClientIP()).Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "too many requests from this IP",
				"retry_after": "1s",
			})
			return
		}

		// Layer 2 — User limit (controls per-user LLM spend)
		// Only applied when auth middleware has set "user_id" in context.
		if userID, exists := c.Get("user_id"); exists {
			if !rl.userStore.get(userID.(string)).Allow() {
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
					"error":       "user rate limit exceeded — please wait before sending another memo",
					"retry_after": "2s",
				})
				return
			}
		}

		c.Next()
	}
}

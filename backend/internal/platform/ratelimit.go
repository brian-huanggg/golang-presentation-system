package platform

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// CodeRateLimited is the stable problem code for a throttled request.
const CodeRateLimited = "rate_limited"

// RateLimitConfig is a token bucket: Burst requests at once, then one more every Refill.
type RateLimitConfig struct {
	Burst  int
	Refill time.Duration
}

// sweepEvery bounds the lazy cleanup; there is no scheduler (Architecture 1.3).
const sweepEvery = time.Minute

// ipLimiter keeps one token bucket per client address, in this process only
// (AUTH-004; a shared limiter is a recorded later option, ADR 0017).
type ipLimiter struct {
	cfg   RateLimitConfig
	clock Clock

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

// RateLimitByIP returns middleware that throttles requests whose path starts with
// pathPrefix, keyed by c.ClientIP(). Which address that is depends on the router's
// trusted proxies, so configure them before relying on this (ADR 0017).
// It is independent of the per-account lockout (AUTH-002/004).
func RateLimitByIP(cfg RateLimitConfig, clock Clock, log *slog.Logger, pathPrefix string) gin.HandlerFunc {
	l := &ipLimiter{cfg: cfg, clock: clock, buckets: map[string]*bucket{}}
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, pathPrefix) {
			c.Next()
			return
		}
		ip := c.ClientIP()
		if l.allow(ip) {
			c.Next()
			return
		}
		log.Warn("rate limit exceeded", "ip", ip, "path", c.Request.URL.Path)
		c.Header("Retry-After", strconv.Itoa(int(math.Ceil(cfg.Refill.Seconds()))))
		WriteProblem(c.Writer, c.Request.URL.Path,
			NewProblem(http.StatusTooManyRequests, CodeRateLimited, "Too many requests. Try again later."))
		c.Abort()
	}
}

func (l *ipLimiter) allow(ip string) bool {
	now := l.clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	b, ok := l.buckets[ip]
	if !ok {
		b = &bucket{lim: rate.NewLimiter(rate.Every(l.cfg.Refill), l.cfg.Burst)}
		l.buckets[ip] = b
	}
	b.seen = now
	return b.lim.AllowN(now, 1)
}

// sweep drops buckets idle long enough to have refilled completely: forgetting
// them changes nothing for the client and keeps memory bounded by active addresses.
func (l *ipLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < sweepEvery {
		return
	}
	l.lastSweep = now
	full := l.cfg.Refill * time.Duration(l.cfg.Burst)
	for ip, b := range l.buckets {
		if now.Sub(b.seen) >= full {
			delete(l.buckets, ip)
		}
	}
}

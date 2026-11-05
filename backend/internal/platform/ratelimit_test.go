package platform

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// newLimitedRouter mimics main.go: trusted proxies first, then the limiter.
func newLimitedRouter(t *testing.T, clock Clock, trusted []string, cfg RateLimitConfig) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(trusted))
	r.Use(RateLimitByIP(cfg, clock, testLog, "/api/v1/auth/"))
	ok := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	r.POST("/api/v1/auth/login", ok)
	r.POST("/api/v1/health", ok)
	return r
}

func hit(r http.Handler, path, remote, xff string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.RemoteAddr = remote
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRateLimit_AUTH_004_blocksAfterBurstWithProblem(t *testing.T) {
	clock := NewManualClock(time.Unix(1_700_000_000, 0))
	r := newLimitedRouter(t, clock, nil, RateLimitConfig{Burst: 3, Refill: 6 * time.Second})

	for i := 0; i < 3; i++ {
		assert.Equal(t, http.StatusNoContent, hit(r, "/api/v1/auth/login", "9.9.9.9:1", "").Code)
	}
	w := hit(r, "/api/v1/auth/login", "9.9.9.9:1", "")
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "6", w.Header().Get("Retry-After"))
	assert.Equal(t, ProblemContentType, w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), `"code":"rate_limited"`)
}

func TestRateLimit_AUTH_004_refillsOverTime(t *testing.T) {
	clock := NewManualClock(time.Unix(1_700_000_000, 0))
	r := newLimitedRouter(t, clock, nil, RateLimitConfig{Burst: 1, Refill: 6 * time.Second})

	require.Equal(t, http.StatusNoContent, hit(r, "/api/v1/auth/login", "9.9.9.9:1", "").Code)
	require.Equal(t, http.StatusTooManyRequests, hit(r, "/api/v1/auth/login", "9.9.9.9:1", "").Code)
	clock.Advance(6 * time.Second)
	assert.Equal(t, http.StatusNoContent, hit(r, "/api/v1/auth/login", "9.9.9.9:1", "").Code)
}

func TestRateLimit_AUTH_004_perAddressAndOnlyAuthPaths(t *testing.T) {
	clock := NewManualClock(time.Unix(1_700_000_000, 0))
	r := newLimitedRouter(t, clock, nil, RateLimitConfig{Burst: 1, Refill: time.Minute})

	require.Equal(t, http.StatusNoContent, hit(r, "/api/v1/auth/login", "1.1.1.1:1", "").Code)
	require.Equal(t, http.StatusTooManyRequests, hit(r, "/api/v1/auth/login", "1.1.1.1:2", "").Code, "port is not part of the key")
	assert.Equal(t, http.StatusNoContent, hit(r, "/api/v1/auth/login", "2.2.2.2:1", "").Code, "other address unaffected")
	for i := 0; i < 5; i++ {
		assert.Equal(t, http.StatusNoContent, hit(r, "/api/v1/health", "1.1.1.1:1", "").Code, "non-auth path is not limited")
	}
}

func TestRateLimit_AUTH_004_ignoresForgedForwardedForFromUntrustedPeer(t *testing.T) {
	clock := NewManualClock(time.Unix(1_700_000_000, 0))
	r := newLimitedRouter(t, clock, nil, RateLimitConfig{Burst: 1, Refill: time.Minute})

	require.Equal(t, http.StatusNoContent, hit(r, "/api/v1/auth/login", "9.9.9.9:1", "10.0.0.1").Code)
	// A new X-Forwarded-For value must not buy a fresh bucket.
	assert.Equal(t, http.StatusTooManyRequests, hit(r, "/api/v1/auth/login", "9.9.9.9:1", "10.0.0.2").Code)
}

func TestRateLimit_AUTH_004_usesForwardedForFromTrustedProxy(t *testing.T) {
	clock := NewManualClock(time.Unix(1_700_000_000, 0))
	r := newLimitedRouter(t, clock, []string{"172.16.0.0/12"}, RateLimitConfig{Burst: 1, Refill: time.Minute})

	require.Equal(t, http.StatusNoContent, hit(r, "/api/v1/auth/login", "172.16.0.5:1", "203.0.113.7").Code)
	assert.Equal(t, http.StatusTooManyRequests, hit(r, "/api/v1/auth/login", "172.16.0.5:1", "203.0.113.7").Code)
	assert.Equal(t, http.StatusNoContent, hit(r, "/api/v1/auth/login", "172.16.0.5:1", "203.0.113.8").Code, "different client behind the proxy")
}

func TestRateLimit_sweepForgetsIdleAddresses(t *testing.T) {
	clock := NewManualClock(time.Unix(1_700_000_000, 0))
	l := &ipLimiter{cfg: RateLimitConfig{Burst: 2, Refill: time.Second}, clock: clock, buckets: map[string]*bucket{}}
	l.allow("1.1.1.1")
	clock.Advance(time.Hour)
	l.allow("2.2.2.2")
	assert.NotContains(t, l.buckets, "1.1.1.1")
	assert.Contains(t, l.buckets, "2.2.2.2")
}

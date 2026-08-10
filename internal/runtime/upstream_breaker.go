package runtime

import (
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	defaultUpstreamBreakerThreshold = 5
	defaultUpstreamBreakerCooldown  = 30 * time.Second
)

// upstreamBreakerState is a process-wide circuit breaker for transient LLM API
// failures. It is keyed by provider so a rate-limited endpoint stops being
// hammered by the main loop and every parallel sub-agent at once.
type upstreamBreakerState struct {
	mu        sync.Mutex
	failures  map[string]int
	openUntil map[string]time.Time
}

var upstreamBreaker = &upstreamBreakerState{
	failures:  make(map[string]int),
	openUntil: make(map[string]time.Time),
}

func upstreamBreakerKey(provider string) string {
	if provider == "" {
		return "default"
	}
	return provider
}

func upstreamBreakerAllow(provider string) bool {
	key := upstreamBreakerKey(provider)
	upstreamBreaker.mu.Lock()
	defer upstreamBreaker.mu.Unlock()
	if until, ok := upstreamBreaker.openUntil[key]; ok && time.Now().Before(until) {
		return false
	}
	return true
}

func upstreamBreakerRecord(provider string, retryable bool) {
	key := upstreamBreakerKey(provider)
	threshold := upstreamBreakerThreshold()
	cooldown := upstreamBreakerCooldown()
	upstreamBreaker.mu.Lock()
	defer upstreamBreaker.mu.Unlock()
	if !retryable {
		delete(upstreamBreaker.failures, key)
		delete(upstreamBreaker.openUntil, key)
		return
	}
	upstreamBreaker.failures[key]++
	if upstreamBreaker.failures[key] >= threshold {
		upstreamBreaker.openUntil[key] = time.Now().Add(cooldown)
		upstreamBreaker.failures[key] = 0
	}
}

func upstreamBreakerThreshold() int {
	if v := os.Getenv("PIGO_UPSTREAM_BREAKER_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultUpstreamBreakerThreshold
}

func upstreamBreakerCooldown() time.Duration {
	if v := os.Getenv("PIGO_UPSTREAM_BREAKER_COOLDOWN_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Millisecond
		}
	}
	return defaultUpstreamBreakerCooldown
}

func resetUpstreamBreakerForTest() {
	upstreamBreaker.mu.Lock()
	defer upstreamBreaker.mu.Unlock()
	upstreamBreaker.failures = make(map[string]int)
	upstreamBreaker.openUntil = make(map[string]time.Time)
}

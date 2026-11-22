package auth

import (
	"sync"
	"time"
)

type RateLimiter struct {
	mu          sync.Mutex
	attempts    map[string][]time.Time
	maxAttempts int
	window      time.Duration
}

func NewRateLimiter(maxAttempts int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		attempts:    make(map[string][]time.Time),
		maxAttempts: maxAttempts,
		window:      window,
	}
}

// Allow returns true if the IP has not exceeded the maximum failed attempts within window.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	times, exists := rl.attempts[ip]
	if !exists {
		return true
	}

	// Filter timestamps within window
	valid := make([]time.Time, 0, len(times))
	for _, t := range times {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	rl.attempts[ip] = valid

	return len(valid) < rl.maxAttempts
}

// RecordFailed records a failed attempt for the given IP.
func (rl *RateLimiter) RecordFailed(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.attempts[ip] = append(rl.attempts[ip], time.Now())
}

// Reset clears recorded failed attempts for the given IP (e.g. on successful login).
func (rl *RateLimiter) Reset(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	delete(rl.attempts, ip)
}

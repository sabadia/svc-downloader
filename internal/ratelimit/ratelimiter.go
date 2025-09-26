package ratelimit

import (
	"context"
	"sync"
	"time"
)

type bucket struct {
	capacity   int64
	tokens     int64
	lastRefill time.Time
}

type SimpleRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

func New() *SimpleRateLimiter { return &SimpleRateLimiter{buckets: make(map[string]*bucket)} }

func (s *SimpleRateLimiter) Reserve(ctx context.Context, key string, bytes int64) (time.Duration, error) {
	s.mu.Lock()
	b, ok := s.buckets[key]
	s.mu.Unlock()
	if !ok || b.capacity <= 0 {
		return 0, nil
	}
	return s.reserveOne(key, bytes)
}

func (s *SimpleRateLimiter) reserveOne(key string, bytes int64) (time.Duration, error) {
	s.mu.Lock()
	b := s.buckets[key]
	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed > 0 {
		refill := int64(float64(b.capacity) * elapsed)
		if refill > 0 {
			b.tokens += refill
			if b.tokens > b.capacity {
				b.tokens = b.capacity
			}
			b.lastRefill = now
		}
	}
	var wait time.Duration
	if b.tokens >= bytes {
		b.tokens -= bytes
		wait = 0
	} else {
		needed := bytes - b.tokens
		b.tokens = 0
		seconds := float64(needed) / float64(b.capacity)
		wait = time.Duration(seconds * float64(time.Second))
	}
	s.mu.Unlock()
	return wait, nil
}

func (s *SimpleRateLimiter) SetLimit(key string, bytesPerSecond int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.buckets[key]
	if !ok {
		b = &bucket{}
		s.buckets[key] = b
	}
	b.capacity = bytesPerSecond
	b.tokens = bytesPerSecond
	b.lastRefill = time.Now()
}

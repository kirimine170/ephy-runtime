package main

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"
)

// Native provider configuration is immutable after construction．Each provider
// owns its cache，so a replacement/configuration cannot reuse another's result．
// File identity is checked even during the TTL，including executable mode changes．
type voiceReadinessKey struct {
	configuration string
	executable    os.FileInfo
}

func (k voiceReadinessKey) matches(other voiceReadinessKey) bool {
	if k.configuration != other.configuration {
		return false
	}
	if k.executable == nil || other.executable == nil {
		return k.executable == nil && other.executable == nil
	}
	return os.SameFile(k.executable, other.executable) && k.executable.Size() == other.executable.Size() &&
		k.executable.Mode() == other.executable.Mode() && k.executable.ModTime().Equal(other.executable.ModTime())
}

type voiceReadinessFlight struct {
	key    voiceReadinessKey
	epoch  uint64
	done   chan struct{}
	result VoiceReadiness
	err    error
}

type voiceReadinessCache struct {
	mu      sync.Mutex
	key     voiceReadinessKey
	result  VoiceReadiness
	expires time.Time
	flight  *voiceReadinessFlight
	epoch   uint64
	now     func() time.Time // Optional deterministic clock，set before first use．
}

func (c *voiceReadinessCache) timeNow() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *voiceReadinessCache) invalidate() {
	c.mu.Lock()
	c.epoch++
	c.expires = time.Time{}
	c.mu.Unlock()
}

func (c *voiceReadinessCache) get(ctx context.Context, currentKey func() (voiceReadinessKey, error), readyTTL time.Duration, probe func(context.Context) (VoiceReadiness, error)) (VoiceReadiness, error) {
	for {
		if err := ctx.Err(); err != nil {
			return VoiceReadiness{}, err
		}
		key, err := currentKey()
		if err != nil {
			c.invalidate()
			return VoiceReadiness{}, err
		}
		c.mu.Lock()
		if c.key.matches(key) && c.timeNow().Before(c.expires) {
			result := c.result
			c.mu.Unlock()
			if err := ctx.Err(); err != nil {
				return VoiceReadiness{}, err
			}
			return result, nil
		}
		if flight := c.flight; flight != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return VoiceReadiness{}, ctx.Err()
			case <-flight.done:
				if err := ctx.Err(); err != nil {
					return VoiceReadiness{}, err
				}
				latestKey, keyErr := currentKey()
				if keyErr != nil {
					c.invalidate()
					return VoiceReadiness{}, keyErr
				}
				c.mu.Lock()
				current := flight.epoch == c.epoch
				c.mu.Unlock()
				// A caller canceling its probe does not poison other callers．
				if !current || !flight.key.matches(latestKey) || errors.Is(flight.err, context.Canceled) || errors.Is(flight.err, context.DeadlineExceeded) {
					continue
				}
				return flight.result, flight.err
			}
		}
		flight := &voiceReadinessFlight{key: key, epoch: c.epoch, done: make(chan struct{})}
		c.flight = flight
		c.expires = time.Time{}
		epoch := c.epoch
		c.mu.Unlock()
		result, err := probe(ctx)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		latestKey, keyErr := currentKey()
		c.mu.Lock()
		flight.result, flight.err = result, err
		current := c.epoch == epoch && keyErr == nil && key.matches(latestKey)
		if err == nil && result.CanStart && current {
			ttl := readyTTL
			if result.State == "permission_required" {
				ttl = time.Second
			}
			c.key, c.result, c.expires = key, result, c.timeNow().Add(ttl)
		}
		c.flight = nil
		close(flight.done)
		c.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return VoiceReadiness{}, err
		}
		if keyErr != nil {
			c.invalidate()
			return VoiceReadiness{}, keyErr
		}
		if !current {
			continue
		}
		return result, err
	}
}

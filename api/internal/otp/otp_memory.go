package otp

import (
	"context"
	"sync"
	"time"
)

type signupEntry struct {
	signup  Signup
	expires time.Time
}

type resetEntry struct {
	reset   Reset
	expires time.Time
}

// Memory is an in-process Store with lazy TTL expiry. It is the zero-dependency
// default, suitable for local development and tests. State is per-process, so
// production with multiple API instances should use the Redis implementation.
type Memory struct {
	mu        sync.Mutex
	signups   map[string]signupEntry
	resets    map[string]resetEntry
	cooldowns map[string]time.Time
	now       func() time.Time
}

// NewMemory returns an empty in-memory Store.
func NewMemory() *Memory {
	return &Memory{
		signups:   map[string]signupEntry{},
		resets:    map[string]resetEntry{},
		cooldowns: map[string]time.Time{},
		now:       time.Now,
	}
}

func (m *Memory) PutSignup(_ context.Context, s Signup, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signups[s.Email] = signupEntry{signup: s, expires: m.now().Add(ttl)}
	return nil
}

func (m *Memory) GetSignup(_ context.Context, email string) (Signup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.signups[email]
	if !ok || m.now().After(e.expires) {
		delete(m.signups, email)
		return Signup{}, ErrNotFound
	}
	return e.signup, nil
}

func (m *Memory) IncrSignupAttempts(_ context.Context, email string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.signups[email]
	if !ok || m.now().After(e.expires) {
		delete(m.signups, email)
		return 0, ErrNotFound
	}
	e.signup.Attempts++
	m.signups[email] = e
	return e.signup.Attempts, nil
}

func (m *Memory) DelSignup(_ context.Context, email string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.signups, email)
	return nil
}

func (m *Memory) PutReset(_ context.Context, r Reset, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resets[r.Email] = resetEntry{reset: r, expires: m.now().Add(ttl)}
	return nil
}

func (m *Memory) GetReset(_ context.Context, email string) (Reset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.resets[email]
	if !ok || m.now().After(e.expires) {
		delete(m.resets, email)
		return Reset{}, ErrNotFound
	}
	return e.reset, nil
}

func (m *Memory) IncrResetAttempts(_ context.Context, email string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.resets[email]
	if !ok || m.now().After(e.expires) {
		delete(m.resets, email)
		return 0, ErrNotFound
	}
	e.reset.Attempts++
	m.resets[email] = e
	return e.reset.Attempts, nil
}

func (m *Memory) DelReset(_ context.Context, email string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.resets, email)
	return nil
}

func (m *Memory) CooldownOK(_ context.Context, key string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if until, ok := m.cooldowns[key]; ok && now.Before(until) {
		return false, nil
	}
	m.cooldowns[key] = now.Add(ttl)
	return true, nil
}

package auth

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Sentinel errors. Use errors.Is to match them.
var (
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrAccountLocked      = errors.New("auth: account temporarily locked")
	ErrRateLimited        = errors.New("auth: rate limit exceeded")
	ErrInvalidToken       = errors.New("auth: invalid token")
	ErrTokenExpired       = errors.New("auth: token expired")
	ErrTokenReuse         = errors.New("auth: refresh token reuse detected")
	ErrUnauthenticated    = errors.New("auth: unauthenticated")
	ErrForbidden          = errors.New("auth: forbidden")
	ErrEmailTaken         = errors.New("auth: email already registered")
	ErrSlugTaken          = errors.New("auth: organization slug already taken")
	ErrAlreadyMember      = errors.New("auth: user is already a member")
	ErrNotFound           = errors.New("auth: not found")
	ErrLastOwner          = errors.New("auth: organization must keep at least one owner")
	ErrValidation         = errors.New("auth: validation failed")
)

// LockedError is returned while an account is locked after too many failed
// logins. It matches ErrAccountLocked.
type LockedError struct{ Until time.Time }

func (e *LockedError) Error() string {
	return fmt.Sprintf("auth: account locked until %s", e.Until.UTC().Format(time.RFC3339))
}
func (e *LockedError) Is(target error) bool { return target == ErrAccountLocked }

// RateLimitedError carries the suggested wait. It matches ErrRateLimited.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("auth: rate limit exceeded, retry in %s", e.RetryAfter.Round(time.Second))
}
func (e *RateLimitedError) Is(target error) bool { return target == ErrRateLimited }

// ValidationError lists invalid input fields. It matches ErrValidation.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+e.Fields[k])
	}
	return "auth: validation failed: " + strings.Join(parts, "; ")
}
func (e *ValidationError) Is(target error) bool { return target == ErrValidation }

func (e *ValidationError) add(field, msg string) {
	if e.Fields == nil {
		e.Fields = map[string]string{}
	}
	if _, ok := e.Fields[field]; !ok {
		e.Fields[field] = msg
	}
}

func (e *ValidationError) orNil() error {
	if e == nil || len(e.Fields) == 0 {
		return nil
	}
	return e
}

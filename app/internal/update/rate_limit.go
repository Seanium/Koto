package update

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RequestError describes a failed HTTP request without treating a refused
// check as evidence that the installed version is current.
type RequestError struct {
	Status      int
	RateLimited bool
	RetryAt     time.Time
}

func (e *RequestError) Code() string {
	if e.RateLimited {
		return "RATE_LIMITED"
	}
	if e.Status == http.StatusForbidden {
		return "HTTP_FORBIDDEN"
	}
	if e.Status == http.StatusServiceUnavailable {
		return "HTTP_UNAVAILABLE"
	}
	return "HTTP_ERROR"
}

func (e *RequestError) Error() string {
	if e.RateLimited {
		return fmt.Sprintf("The update service is limiting requests. Try again after %s, or download from %s", e.RetryAt.Format(time.RFC3339), ReleasesPage)
	}
	if e.Status == http.StatusForbidden {
		return "The update service refused the request (HTTP 403). Try again later, or download from " + ReleasesPage
	}
	if e.Status == http.StatusServiceUnavailable {
		return "The update service is temporarily unavailable. Try again later, or download from " + ReleasesPage
	}
	return fmt.Sprintf("update request failed: HTTP %d; downloads: %s", e.Status, ReleasesPage)
}

func responseError(status int, header http.Header, now time.Time) *RequestError {
	e := &RequestError{Status: status}
	if status == http.StatusServiceUnavailable {
		if retryAt, valid := retryAfter(header.Get("Retry-After"), now); valid && retryAt.After(now) {
			e.RetryAt = retryAt
		}
		return e
	}
	if status != http.StatusForbidden && status != http.StatusTooManyRequests {
		return e
	}
	remainingZero := strings.TrimSpace(header.Get("X-RateLimit-Remaining")) == "0"
	retryAt, retryValid := retryAfter(header.Get("Retry-After"), now)
	e.RateLimited = status == http.StatusTooManyRequests || remainingZero || retryValid
	if !e.RateLimited {
		return e
	}
	e.RetryAt = retryAt
	if remainingZero {
		if seconds, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64); err == nil && seconds > 0 {
			reset := time.Unix(seconds, 0)
			if reset.After(e.RetryAt) {
				e.RetryAt = reset
			}
		}
	}
	// Missing, malformed or already elapsed hints must not cause a tight retry
	// loop. GitHub recommends at least one minute for secondary limits.
	if !e.RetryAt.After(now) {
		e.RetryAt = now.Add(time.Minute)
	}
	return e
}

func retryAfter(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 || seconds > int64((1<<63-1)/time.Second) {
			return time.Time{}, false
		}
		return now.Add(time.Duration(seconds) * time.Second), true
	}
	if date, err := http.ParseTime(value); err == nil {
		return date, true
	}
	return time.Time{}, false
}

func (c *Client) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Client) cooldown(host string) error {
	c.rateMu.Lock()
	defer c.rateMu.Unlock()
	if e := c.rateLimits[host]; e != nil {
		if c.clock().Before(e.RetryAt) {
			return e
		}
		delete(c.rateLimits, host)
	}
	return nil
}

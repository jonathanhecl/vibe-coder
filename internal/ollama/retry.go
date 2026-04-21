package ollama

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

const maxChatHTTPAttempts = 3

func chatHTTPBackoff(attempt int) time.Duration {
	// attempt 0 = delay before 2nd try
	return time.Duration(400*(1<<attempt)) * time.Millisecond
}

// shouldRetryChatHTTP reports whether the failed initial POST to /api/chat may succeed on retry.
func shouldRetryChatHTTP(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// Only retry if this request's context is not already done.
		return ctx.Err() == nil
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var op *net.OpError
	if errors.As(err, &op) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNABORTED) {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, sub := range []string{
		"connection reset",
		"connection refused",
		"broken pipe",
		"eof",
		"tls:",
		"use of closed network connection",
		"no such host",
		"i/o timeout",
		"websocket",
	} {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func doPOSTWithRetry(ctx context.Context, hc *http.Client, newReq func() (*http.Request, error)) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < maxChatHTTPAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(chatHTTPBackoff(attempt - 1)):
			}
		}
		httpReq, err := newReq()
		if err != nil {
			return nil, err
		}
		resp, err := hc.Do(httpReq)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !shouldRetryChatHTTP(ctx, err) {
			break
		}
	}
	return nil, lastErr
}

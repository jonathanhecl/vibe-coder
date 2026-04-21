package ollama

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShouldRetryChatHTTP(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if shouldRetryChatHTTP(ctx, nil) {
		t.Fatal("nil err")
	}
	if shouldRetryChatHTTP(ctx, context.Canceled) {
		t.Fatal("canceled")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if shouldRetryChatHTTP(cancelled, fmt.Errorf("wrap: %w", errors.New("eof"))) {
		t.Fatal("ctx done")
	}
	if !shouldRetryChatHTTP(ctx, errors.New("connection reset by peer")) {
		t.Fatal("expected retry on reset string")
	}
	if !shouldRetryChatHTTP(ctx, &net.OpError{Op: "dial", Err: errors.New("refused")}) {
		t.Fatal("expected retry on op error")
	}
}

func TestDoPOSTWithRetryUsesSuccessfulResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{}
	ctx := context.Background()
	resp, err := doPOSTWithRetry(ctx, client, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

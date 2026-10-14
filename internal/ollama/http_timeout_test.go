package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestDeadEndpointFailsFast verifies that a server which accepts the
// connection but never sends response headers cannot hang a call forever: the
// transport's response-header timeout turns it into an error.
func TestDeadEndpointFailsFast(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer srv.Close()
	defer close(block)

	client := &HTTPClient{
		baseURL: NormalizeURL(srv.URL),
		http: &http.Client{
			Transport: transportWithHeaderTimeout(50 * time.Millisecond),
		},
		thinkDisabledModels: make(map[string]bool),
		toolsDisabledModels: make(map[string]bool),
	}

	start := time.Now()
	_, err := client.Tags(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from an endpoint that never responds")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("call took %s, expected it to fail fast", elapsed)
	}
}

// TestDefaultTransportBoundsConnections asserts the production transport
// bounds connection establishment while leaving the overall request uncapped,
// so long generations are never cut short.
func TestDefaultTransportBoundsConnections(t *testing.T) {
	t.Parallel()

	tr := defaultTransport()
	if tr.DialContext == nil {
		t.Fatal("expected a dial timeout to be configured")
	}
	if tr.TLSHandshakeTimeout <= 0 {
		t.Fatalf("expected a TLS handshake timeout, got %s", tr.TLSHandshakeTimeout)
	}
	if tr.ResponseHeaderTimeout <= 0 {
		t.Fatalf("expected a response-header timeout, got %s", tr.ResponseHeaderTimeout)
	}
	if defaultHTTPTimeout != 0 {
		t.Fatalf("expected the overall request timeout to stay uncapped, got %d", defaultHTTPTimeout)
	}
}

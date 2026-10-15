package lsp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestWriteMessageFramesPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMessage(&buf, []byte(`{"jsonrpc":"2.0"}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	want := "Content-Length: 17\r\n\r\n{\"jsonrpc\":\"2.0\"}"
	if got := buf.String(); got != want {
		t.Fatalf("frame = %q, want %q", got, want)
	}
}

func TestReadMessageAcceptsHeaderVariants(t *testing.T) {
	tests := []struct {
		name  string
		frame string
	}{
		{name: "exact header", frame: "Content-Length: 2\r\n\r\n{}"},
		{name: "lowercase header name", frame: "content-length: 2\r\n\r\n{}"},
		{name: "extra headers ignored", frame: "Content-Type: application/vscode-jsonrpc; charset=utf-8\r\nContent-Length: 2\r\n\r\n{}"},
		{name: "bare line feed", frame: "Content-Length: 2\n\n{}"},
		{name: "padded value", frame: "Content-Length:  2  \r\n\r\n{}"},
		{name: "header after an unknown one", frame: "X-Trace: 1\r\nContent-Length: 2\r\n\r\n{}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadMessage(bufio.NewReader(strings.NewReader(tt.frame)))
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}
			if string(got) != "{}" {
				t.Fatalf("payload = %q, want %q", got, "{}")
			}
		})
	}
}

func TestReadMessageRejectsBadFrames(t *testing.T) {
	tests := []struct {
		name  string
		frame string
		want  error
	}{
		{name: "no length", frame: "Content-Type: application/json\r\n\r\n{}", want: errMissingLength},
		{name: "no headers", frame: "\r\n{}", want: errMissingLength},
		{name: "length not a number", frame: "Content-Length: two\r\n\r\n{}", want: errBadLength},
		{name: "negative length", frame: "Content-Length: -1\r\n\r\n{}", want: errBadLength},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := ReadMessage(bufio.NewReader(strings.NewReader(tt.frame)))
			if err == nil {
				t.Fatalf("ReadMessage returned %q, want an error", payload)
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want it to wrap %v", err, tt.want)
			}
		})
	}
}

func TestReadMessageRejectsOversizedMessage(t *testing.T) {
	// The length is only announced, never sent: a client that allocates from
	// the header alone would reserve 32 MiB to satisfy this frame.
	frame := "Content-Length: 33554433\r\n\r\n"
	_, err := ReadMessage(bufio.NewReader(strings.NewReader(frame)))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v, want an oversized message error", err)
	}
}

func TestReadMessageReportsTruncatedPayload(t *testing.T) {
	frame := "Content-Length: 10\r\n\r\n{}"
	if _, err := ReadMessage(bufio.NewReader(strings.NewReader(frame))); err == nil {
		t.Fatal("ReadMessage accepted a payload shorter than its declared length")
	}
}

func TestReadMessageReportsCleanEndOfStream(t *testing.T) {
	if _, err := ReadMessage(bufio.NewReader(strings.NewReader(""))); !errors.Is(err, io.EOF) {
		t.Fatalf("error = %v, want io.EOF", err)
	}
}

func TestReadMessageRoundTripsPayloadWithLineBreaks(t *testing.T) {
	payload := []byte("{\"text\":\"line one\r\nline two\nña\"}")
	var buf bytes.Buffer
	if err := WriteMessage(&buf, payload); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	got, err := ReadMessage(bufio.NewReader(&buf))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}

func TestReadMessageReadsConsecutiveFrames(t *testing.T) {
	var buf bytes.Buffer
	for _, payload := range []string{"{}", `{"a":1}`, "[]"} {
		if err := WriteMessage(&buf, []byte(payload)); err != nil {
			t.Fatalf("WriteMessage: %v", err)
		}
	}
	reader := bufio.NewReader(&buf)
	for _, want := range []string{"{}", `{"a":1}`, "[]"} {
		got, err := ReadMessage(reader)
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		if string(got) != want {
			t.Fatalf("payload = %q, want %q", got, want)
		}
	}
}

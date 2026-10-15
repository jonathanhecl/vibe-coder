package lsp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// maxMessageBytes bounds a single message. A language server that reports a
// bogus Content-Length must not be able to make the client allocate an
// arbitrary amount of memory.
const maxMessageBytes = 32 << 20

var (
	// errMissingLength marks a frame that carried headers but no length, which
	// means the stream is out of sync and cannot be trusted further.
	errMissingLength = errors.New("lsp: message without Content-Length")

	// errBadLength marks a length that is not a usable byte count.
	errBadLength = errors.New("lsp: invalid Content-Length")
)

// WriteMessage writes one LSP frame: headers, a blank line, then the payload.
// Content-Length is the only header the protocol requires; Content-Type is
// optional and deliberately not sent, because every mainstream server reads
// the length alone.
func WriteMessage(w io.Writer, payload []byte) error {
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return fmt.Errorf("lsp: write header: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("lsp: write payload: %w", err)
	}
	return nil
}

// ReadMessage reads one LSP frame. Header names are case-insensitive, unknown
// headers are skipped and a bare LF line ending is accepted, because the
// specification allows all three and real servers rely on it.
func ReadMessage(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			// A clean stream end between frames is how a server says it is
			// done, and it is not an error the caller should see as one.
			if errors.Is(err, io.EOF) && line == "" {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("lsp: read header: %w", err)
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			break
		}
		name, value, ok := strings.Cut(trimmed, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		parsed, convErr := strconv.Atoi(strings.TrimSpace(value))
		if convErr != nil || parsed < 0 {
			return nil, fmt.Errorf("%w: %q", errBadLength, strings.TrimSpace(value))
		}
		length = parsed
	}
	if length < 0 {
		return nil, errMissingLength
	}
	if length > maxMessageBytes {
		return nil, fmt.Errorf("lsp: message of %d bytes exceeds the %d byte limit", length, maxMessageBytes)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("lsp: read payload: %w", err)
	}
	return payload, nil
}

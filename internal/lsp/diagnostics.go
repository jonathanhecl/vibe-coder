package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Diagnostic severities, as the protocol numbers them.
const (
	SeverityError       = 1
	SeverityWarning     = 2
	SeverityInformation = 3
	SeverityHint        = 4
)

// versionUnknown marks a published diagnostic list whose notification carried
// no document version.
const versionUnknown = -1

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// DiagnosticCode is a diagnostic's code as reported. The specification allows a
// string or a number and servers use both, so decoding it as either single
// shape would throw away the whole notification.
type DiagnosticCode struct {
	Value string
}

func (c *DiagnosticCode) UnmarshalJSON(raw []byte) error {
	if string(raw) == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		c.Value = text
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		c.Value = number.String()
		return nil
	}
	return fmt.Errorf("lsp: unsupported diagnostic code %s", raw)
}

type Diagnostic struct {
	Range    Range          `json:"range"`
	Severity int            `json:"severity,omitempty"`
	Code     DiagnosticCode `json:"code,omitempty"`
	Source   string         `json:"source,omitempty"`
	Message  string         `json:"message"`
}

// Label is the severity in the vocabulary every model-facing rendering uses.
// A server that omits severity is reported as unknown rather than guessed at.
func (d Diagnostic) Label() string {
	switch d.Severity {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInformation:
		return "info"
	case SeverityHint:
		return "hint"
	default:
		return "unknown"
	}
}

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// parsePublishDiagnostics decodes a publishDiagnostics notification. The
// version is optional in the protocol, so it is reported as absent rather than
// as zero, which would read as a real document version.
func parsePublishDiagnostics(raw json.RawMessage) (string, *int, []Diagnostic, error) {
	var params publishDiagnosticsParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return "", nil, nil, fmt.Errorf("lsp: decode diagnostics: %w", err)
	}
	if params.URI == "" {
		return "", nil, nil, fmt.Errorf("lsp: diagnostics without a document uri")
	}
	return params.URI, params.Version, params.Diagnostics, nil
}

// documentDiagnostics is the last list published for one document, together
// with the version it describes. The version is what makes waiting
// deterministic: a notification that describes an older version is still in
// flight from a previous edit and must not be mistaken for the answer to the
// current one.
type documentDiagnostics struct {
	diagnostics []Diagnostic
	version     int
}

// diagnosticsWaiter is one pending wait, signalled by the next publication for
// its document.
type diagnosticsWaiter struct {
	uri        string
	minVersion int
	ch         chan struct{}
}

// diagnosticsStore holds the last published list per document and wakes the
// callers waiting for it. Diagnostics are push-only in the protocol, so every
// wait is a race against a notification that may never come; the store's job is
// to never lose one that did.
type diagnosticsStore struct {
	mu      sync.Mutex
	byURI   map[string]documentDiagnostics
	waiters map[*diagnosticsWaiter]struct{}
}

func newDiagnosticsStore() *diagnosticsStore {
	return &diagnosticsStore{
		byURI:   map[string]documentDiagnostics{},
		waiters: map[*diagnosticsWaiter]struct{}{},
	}
}

// handlePublish stores a notification. A malformed list is dropped instead of
// failing: one bad notification must not cost the session its diagnostics.
func (s *diagnosticsStore) handlePublish(raw json.RawMessage) {
	uri, version, diagnostics, err := parsePublishDiagnostics(raw)
	if err != nil {
		return
	}
	s.publish(uri, version, diagnostics)
}

func (s *diagnosticsStore) publish(uri string, version *int, diagnostics []Diagnostic) {
	entry := documentDiagnostics{diagnostics: diagnostics, version: versionUnknown}
	if version != nil {
		entry.version = *version
	}
	if diagnostics == nil {
		// A publish with no diagnostics is a real, meaningful answer: the
		// document is clean. Keeping the nil slice would render as nothing.
		entry.diagnostics = []Diagnostic{}
	}

	s.mu.Lock()
	s.byURI[uri] = entry
	waiters := make([]*diagnosticsWaiter, 0, len(s.waiters))
	for w := range s.waiters {
		if w.uri == uri {
			waiters = append(waiters, w)
		}
	}
	s.mu.Unlock()

	for _, w := range waiters {
		select {
		case w.ch <- struct{}{}:
		default: // already signalled; the branch reads the state, not the signal
		}
	}
}

func (s *diagnosticsStore) unsubscribe(w *diagnosticsWaiter) {
	s.mu.Lock()
	delete(s.waiters, w)
	s.mu.Unlock()
}

// latest returns the last known list for a document.
func (s *diagnosticsStore) latest(uri string) ([]Diagnostic, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.byURI[uri]
	if !ok {
		return nil, false
	}
	return entry.diagnostics, true
}

// wait blocks until a publication describing at least minVersion arrives. It
// reports whether such a publication was seen: a clean document is a
// successful wait with an empty list, while a timeout returns whatever was
// known before, flagged as not an answer to this edit.
func (s *diagnosticsStore) wait(ctx context.Context, uri string, minVersion int, timeout time.Duration) ([]Diagnostic, bool) {
	w := &diagnosticsWaiter{uri: uri, minVersion: minVersion, ch: make(chan struct{}, 1)}

	s.mu.Lock()
	// Subscribe before checking. A notification landing between the check and
	// the subscribe would otherwise be lost, and the caller would wait out the
	// whole timeout for an answer that had already arrived. A caller that asks
	// for no particular version takes whatever is known; a caller that names
	// one waits for it rather than accept a list published for an older
	// revision of the same file.
	if entry, ok := s.byURI[uri]; ok {
		if minVersion <= 0 || (entry.version != versionUnknown && entry.version >= minVersion) {
			s.mu.Unlock()
			return entry.diagnostics, true
		}
	}
	s.waiters[w] = struct{}{}
	s.mu.Unlock()
	defer s.unsubscribe(w)

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-w.ch:
			s.mu.Lock()
			entry, ok := s.byURI[uri]
			s.mu.Unlock()
			if !ok {
				continue
			}
			// A publication for an older version is still in flight from a
			// previous edit; keep waiting for the one that describes the
			// version just sent. A server that reports no version is taken at
			// its word, because there is nothing to compare against.
			if entry.version != versionUnknown && minVersion > 0 && entry.version < minVersion {
				continue
			}
			return entry.diagnostics, true
		case <-timer.C:
			diagnostics, _ := s.latest(uri)
			return diagnostics, false
		case <-ctx.Done():
			diagnostics, _ := s.latest(uri)
			return diagnostics, false
		}
	}
}

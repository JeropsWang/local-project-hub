package process

import (
	"bytes"
	"io"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
)

func logSecrets(values []string) ([]string, error) {
	if len(values) > 128 {
		return nil, problem.New("LOG_SECRET_LIMIT", "")
	}
	result := []string{}
	for _, value := range values {
		if value == "" {
			continue
		}
		if len(value) > 64*1024 {
			return nil, problem.New("LOG_SECRET_LIMIT", "")
		}
		if !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	// Redact longer overlapping values before a shorter prefix can expose a tail.
	sort.Slice(result, func(i, j int) bool { return len(result[i]) > len(result[j]) })
	return result, nil
}

// redactingLogWriter retains enough lookahead to hide a known secret even when
// stdout splits it across writes or newline boundaries. Only sanitized bytes go to disk.
type redactingLogWriter struct {
	mu      sync.Mutex
	log     *RunLog
	stream  string
	secrets []string
	pending []byte
	line    []byte
	closed  bool
	err     error
}

func (w *redactingLogWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	length := len(data)
	for len(data) > 0 {
		amount := min(len(data), 32*1024)
		w.pending = append(w.pending, data[:amount]...)
		data = data[amount:]
		w.drain(false)
	}
	// Keep draining child output after a disk error so a full pipe cannot deadlock
	// application shutdown; Close reports the controlled logging failure.
	return length, nil
}

func (w *redactingLogWriter) drain(final bool) {
	for len(w.pending) > 0 {
		matched := false
		for _, secret := range w.secrets {
			// Retain only a possible secret prefix. Unrelated short lines can
			// appear immediately while a long-running process is still alive.
			if !final && len(w.pending) < len(secret) && bytes.HasPrefix([]byte(secret), w.pending) {
				return
			}
			if bytes.HasPrefix(w.pending, []byte(secret)) {
				w.emit([]byte("[REDACTED]"))
				w.pending = w.pending[len(secret):]
				matched = true
				break
			}
		}
		if !matched {
			w.emit(w.pending[:1])
			w.pending = w.pending[1:]
		}
	}
}

func (w *redactingLogWriter) emit(data []byte) {
	if w.err != nil {
		return
	}
	for _, value := range data {
		if value == '\n' {
			w.err = w.log.append(w.stream, trimLogLine(w.line))
			w.line = w.line[:0]
			continue
		}
		w.line = append(w.line, value)
		if len(w.line) == maxLogLine {
			w.err = w.log.append(w.stream, string(w.line))
			w.line = w.line[:0]
		}
	}
}

func (w *redactingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return w.err
	}
	w.closed = true
	w.drain(true)
	if len(w.line) > 0 && w.err == nil {
		w.err = w.log.append(w.stream, strings.TrimSuffix(string(w.line), "\r"))
		w.line = nil
	}
	return w.err
}

package process

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/adapters/filesystem"
	"github.com/JeropsWang/local-project-hub/internal/domain/control"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

const logSegmentBytes int64 = 10 * 1024 * 1024
const logSegments = 3
const maxLogLine = 64 * 1024

// RunLog rotates only its own run's files, after known secrets have been redacted.
type RunLog struct {
	mu           sync.Mutex
	root         *os.Root
	file         *os.File
	size         int64
	sequence     uint64
	segmentBytes int64
	segments     int
	secrets      []string
	writers      []*redactingLogWriter
	closed       bool
}

// NewRunLog creates a fresh run log with 10MiB segments and three retained segments.
// Reusing a run identity is rejected instead of truncating its previous evidence.
func NewRunLog(workspace, runID string, secrets []string) (*RunLog, error) {
	return newRunLog(workspace, runID, secrets, logSegmentBytes, logSegments)
}

func newRunLog(workspace, runID string, secrets []string, segmentBytes int64, segments int) (*RunLog, error) {
	if !control.ValidIdentity(runID) || segmentBytes < 256 || segmentBytes > logSegmentBytes || segments < 1 ||
		segments > logSegments {
		return nil, problem.New("LOG_CONFIGURATION_INVALID", "")
	}
	clean, err := logSecrets(secrets)
	if err != nil {
		return nil, err
	}
	anchor, err := filesystem.OpenLocalRoot(workspace)
	if err != nil {
		return nil, err
	}
	defer anchor.Close()
	if err := anchor.MkdirAll("logs", 0700); err != nil {
		return nil, err
	}
	relative := path.Join("logs", runID)
	if err := anchor.Mkdir(relative, 0700); err != nil {
		return nil, problem.New("RUN_LOG_EXISTS", "")
	}
	root, err := anchor.OpenRoot(relative)
	if err != nil {
		return nil, err
	}
	file, err := root.OpenFile("current.jsonl", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		root.Close()
		return nil, err
	}
	return &RunLog{root: root, file: file, segmentBytes: segmentBytes, segments: segments, secrets: clean}, nil
}

// Writer must be closed to flush the last partial line. RunLog.Close also closes
// registered writers, ensuring a final token tail cannot reach disk unredacted.
func (l *RunLog) Writer(stream string) io.WriteCloser {
	if stream != "stdout" && stream != "stderr" {
		stream = "system"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w := &redactingLogWriter{log: l, stream: stream, secrets: l.secrets, closed: l.closed}
	l.writers = append(l.writers, w)
	return w
}

// Close preserves all completed redacted lines before releasing file handles.
func (l *RunLog) Close() error {
	l.mu.Lock()
	writers := append([]*redactingLogWriter(nil), l.writers...)
	l.mu.Unlock()
	var result error
	for _, writer := range writers {
		result = errors.Join(result, writer.Close())
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return result
	}
	l.closed = true
	return errors.Join(result, l.file.Sync(), l.file.Close(), l.root.Close())
}

func (l *RunLog) append(stream, text string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return io.ErrClosedPipe
	}
	entry := run.LogEntry{
		Seq:        strconv.FormatUint(l.sequence+1, 10),
		Stream:     stream,
		Text:       text,
		OccurredAt: time.Now().UTC(),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if int64(len(data)) > l.segmentBytes {
		return problem.New("LOG_ENTRY_LIMIT", "")
	}
	if l.size+int64(len(data)) > l.segmentBytes {
		if err := l.rotate(); err != nil {
			return err
		}
	}
	n, err := l.file.Write(data)
	l.size += int64(n)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		l.sequence++
	}
	return err
}

func (l *RunLog) rotate() error {
	if err := l.file.Sync(); err != nil {
		return err
	}
	if err := l.file.Close(); err != nil {
		return err
	}
	oldest := "current.jsonl"
	if l.segments > 1 {
		oldest = strconv.Itoa(l.segments-1) + ".jsonl"
	}
	if err := l.root.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return err
	}
	for number := l.segments - 2; number >= 1; number-- {
		if err := l.root.Rename(strconv.Itoa(number)+".jsonl", strconv.Itoa(number+1)+".jsonl"); err != nil &&
			!os.IsNotExist(err) {
			return err
		}
	}
	if l.segments > 1 {
		if err := l.root.Rename("current.jsonl", "1.jsonl"); err != nil {
			return err
		}
	}
	file, err := l.root.OpenFile("current.jsonl", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	l.file = file
	l.size = 0
	return nil
}

// ReadRunLog returns at most 2000 entries without loading entire log segments into memory.
func ReadRunLog(workspace, runID, after string, limit int) (run.LogPage, error) {
	page := run.LogPage{Entries: []run.LogEntry{}, Next: after}
	if after == "" {
		after = "0"
		page.Next = after
	}
	cursor, err := strconv.ParseUint(after, 10, 64)
	if err != nil || strconv.FormatUint(cursor, 10) != after || limit < 1 || limit > 2000 ||
		!control.ValidIdentity(runID) {
		return page, problem.New("LOG_QUERY_INVALID", "")
	}
	root, err := filesystem.OpenLocalRoot(filepath.Join(workspace, "logs", runID))
	if err != nil {
		return page, err
	}
	defer root.Close()
	var previous uint64
	first := true
	for _, name := range []string{"2.jsonl", "1.jsonl", "current.jsonl"} {
		info, err := root.Lstat(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return page, err
		}
		if !info.Mode().IsRegular() || info.Size() > logSegmentBytes {
			return page, problem.New("LOG_FILE_INVALID", "")
		}
		file, err := root.Open(name)
		if err != nil {
			return page, err
		}
		scanner := bufio.NewScanner(io.LimitReader(file, logSegmentBytes+1))
		scanner.Buffer(make([]byte, 4096), 512*1024)
		for scanner.Scan() {
			var entry run.LogEntry
			if json.Unmarshal(scanner.Bytes(), &entry) != nil {
				file.Close()
				return page, problem.New("LOG_FILE_INVALID", "")
			}
			seq, err := strconv.ParseUint(entry.Seq, 10, 64)
			if err != nil || seq == 0 || strconv.FormatUint(seq, 10) != entry.Seq || !first && seq <= previous ||
				len(entry.Text) > maxLogLine {
				file.Close()
				return page, problem.New("LOG_FILE_INVALID", "")
			}
			if first && seq > cursor && seq-cursor > 1 {
				page.Gap = true
			}
			first = false
			previous = seq
			if seq <= cursor {
				continue
			}
			page.Entries = append(page.Entries, entry)
			page.Next = entry.Seq
			if len(page.Entries) == limit {
				file.Close()
				return page, nil
			}
		}
		err = scanner.Err()
		file.Close()
		if err != nil {
			return page, err
		}
	}
	return page, nil
}

func trimLogLine(data []byte) string { return strings.TrimSuffix(string(data), "\r") }

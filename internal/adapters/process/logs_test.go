package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveLogReadsStayConsistentDuringRotation(t *testing.T) {
	root := t.TempDir()
	log, err := newRunLog(root, fixtureControlRun, nil, 512, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	writer := log.Writer("stdout")
	done := make(chan error, 1)
	go func() {
		for index := 0; index < 400; index++ {
			if _, err := fmt.Fprintf(writer, "line %03d payload\n", index); err != nil {
				done <- err
				return
			}
		}
		done <- writer.Close()
	}()
	cursor := "0"
	for index := 0; index < 400; index++ {
		page, err := log.Read(cursor, 20)
		if err != nil {
			t.Fatal("rotation produced an invalid live page", err)
		}
		cursor = page.Next
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	page, err := log.Read(cursor, 2000)
	if err != nil || page.Next != "400" {
		t.Fatalf("final live cursor: %+v %v", page, err)
	}
}

func TestRunLogRedactsSecretsAcrossWritesAndFinalTail(t *testing.T) {
	root := t.TempDir()
	log, err := NewRunLog(root, fixtureControlRun, []string{"fixture-secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	writer := log.Writer("stdout")
	for _, part := range []string{"before fixture-sec", "ret-value after\n", "fixture-secret-value"} {
		if _, err := writer.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	page, err := ReadRunLog(root, fixtureControlRun, "0", 2000)
	if err != nil || len(page.Entries) != 2 || page.Entries[0].Text != "before [REDACTED] after" ||
		page.Entries[1].Text != "[REDACTED]" {
		t.Fatalf("streamed redaction: %+v %v", page, err)
	}
	files, err := filepath.Glob(filepath.Join(root, "logs", fixtureControlRun, "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil || strings.Contains(string(data), "fixture-secret-value") ||
			strings.Contains(string(data), "fixture-sec") {
			t.Fatalf("secret reached disk: %s %v", name, err)
		}
	}
}

func TestRunLogRotationIsBoundedAndReportsCursorGap(t *testing.T) {
	root := t.TempDir()
	log, err := newRunLog(root, fixtureControlRun, nil, 300, 3)
	if err != nil {
		t.Fatal(err)
	}
	writer := log.Writer("stderr")
	for i := 0; i < 100; i++ {
		if _, err := writer.Write([]byte(strings.Repeat("x", 70) + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	page, err := ReadRunLog(root, fixtureControlRun, "0", 2000)
	if err != nil || !page.Gap || len(page.Entries) == 0 || len(page.Entries) >= 100 || page.Next != "100" {
		t.Fatalf("rotated cursors: %+v %v", page, err)
	}
	files, err := filepath.Glob(filepath.Join(root, "logs", fixtureControlRun, "*.jsonl"))
	if err != nil || len(files) > 3 {
		t.Fatalf("unbounded segments: %v %v", files, err)
	}
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil || info.Size() > 300 {
			t.Fatal("segment budget exceeded", err)
		}
	}
	if _, err := ReadRunLog(root, fixtureControlRun, "0", 2001); err == nil {
		t.Fatal("UI page budget ignored")
	}
}

func TestRunLogPublishesShortUnrelatedLinesBeforeProcessExit(t *testing.T) {
	root := t.TempDir()
	log, err := NewRunLog(root, fixtureControlRun, []string{fixtureControlToken})
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	writer := log.Writer("stdout")
	if _, err := writer.Write([]byte("ready\n")); err != nil {
		t.Fatal(err)
	}
	page, err := ReadRunLog(root, fixtureControlRun, "0", 100)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Text != "ready" {
		t.Fatalf("live short log held until exit: %+v %v", page, err)
	}
}

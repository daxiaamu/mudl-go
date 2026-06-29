package main

import (
	"net/http"
	"testing"
)

func TestFilenameFromContentDisposition(t *testing.T) {
	headers := http.Header{}
	headers.Set("Content-Disposition", "attachment; filename*=UTF-8''%E4%B8%80%E5%8A%A0%E5%85%A8%E8%83%BD%E5%B7%A5%E5%85%B7%E7%AE%B121.2.exe")

	got := filenameFromResponse(headers, "https://example.com/hash.exe?fileName=wrong.exe", "")
	want := "一加全能工具箱21.2.exe"

	if got != want {
		t.Fatalf("filename = %q, want %q", got, want)
	}
}

func TestSchedulerStealsTailFromActiveTask(t *testing.T) {
	s := newScheduler(1024, 2, 64, 1024)
	first := s.nextTask()
	if first == nil {
		t.Fatal("first task is nil")
	}
	s.pending = nil
	first.next = 128
	first.end = 1023

	stolen := s.nextTask()
	if stolen == nil {
		t.Fatal("expected stolen task")
	}
	if stolen.start <= first.end {
		t.Fatalf("stolen range overlaps victim: stolen start %d, victim end %d", stolen.start, first.end)
	}
	if s.steals() != 1 {
		t.Fatalf("steals = %d, want 1", s.steals())
	}
}

func TestParseContentRangeSize(t *testing.T) {
	got, ok := parseContentRangeSize("bytes 0-0/90092968")
	if !ok {
		t.Fatal("expected content range to parse")
	}
	if got != 90092968 {
		t.Fatalf("size = %d, want 90092968", got)
	}
}

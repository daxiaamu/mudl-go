package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

func TestSchedulerStartsLikeNDMBisection(t *testing.T) {
	s := newScheduler(1024, 8, 64, 1024)
	var starts []int64
	for len(starts) < 8 {
		task := s.nextTask()
		if task == nil {
			t.Fatal("expected task")
		}
		starts = append(starts, task.start)
	}

	want := []int64{0, 512, 768, 256, 896, 640, 384, 128}
	for i := range want {
		if starts[i] != want[i] {
			t.Fatalf("starts[%d] = %d, want %d; all starts=%v", i, starts[i], want[i], starts)
		}
	}
}

func TestSchedulerDoesNotStealReservedReadWindow(t *testing.T) {
	s := newScheduler(1024, 2, 64, 1024)
	first := s.nextTask()
	if first == nil {
		t.Fatal("first task is nil")
	}
	if _, ok := s.beginRead(first, 256); !ok {
		t.Fatal("expected read reservation")
	}

	stolen := s.nextTask()
	if stolen == nil {
		t.Fatal("expected stolen task")
	}
	if stolen.start <= first.reserved {
		t.Fatalf("stolen start %d overlaps reserved end %d", stolen.start, first.reserved)
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

func TestProbeUsesCustomUserAgent(t *testing.T) {
	const wantUA = "custom-mudl"
	seenUA := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Length", "1")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if _, err := probe(server.Client(), server.URL, wantUA); err != nil {
		t.Fatal(err)
	}
	if seenUA != wantUA {
		t.Fatalf("User-Agent = %q, want %q", seenUA, wantUA)
	}
}

func TestDownloadUsesOpenEndedDynamicRanges(t *testing.T) {
	data := make([]byte, 512*1024)
	for i := range data {
		data[i] = byte(i % 251)
	}

	var mu sync.Mutex
	var ranges []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Content-Disposition", `attachment; filename="range-test.bin"`)
		if r.Method == http.MethodHead {
			return
		}

		rangeHeader := r.Header.Get("Range")
		mu.Lock()
		ranges = append(ranges, rangeHeader)
		mu.Unlock()

		start, end, partial, ok := testParseRange(rangeHeader, int64(len(data)))
		if !ok {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if partial {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
			w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
			w.WriteHeader(http.StatusPartialContent)
		}
		_, _ = w.Write(data[start : end+1])
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "out.bin")
	saved, err := download(context.Background(), server.Client(), server.URL+"/file.bin", output, 4, 32*1024, 1024*1024, 16*1024, 256*1024, 2, "mudl-test", "none")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("downloaded data mismatch")
	}

	mu.Lock()
	defer mu.Unlock()
	openEnded := 0
	for _, r := range ranges {
		if strings.HasPrefix(r, "bytes=") && strings.HasSuffix(r, "-") {
			openEnded++
		}
	}
	if openEnded == 0 {
		t.Fatalf("expected open-ended dynamic Range requests, got %v", ranges)
	}
}

func testParseRange(value string, size int64) (int64, int64, bool, bool) {
	if value == "" {
		return 0, size - 1, false, true
	}
	if !strings.HasPrefix(value, "bytes=") {
		return 0, 0, false, false
	}
	parts := strings.SplitN(strings.TrimPrefix(value, "bytes="), "-", 2)
	if len(parts) != 2 {
		return 0, 0, false, false
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false, false
	}
	end := size - 1
	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start {
			return 0, 0, false, false
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true, true
}

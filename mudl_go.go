package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const defaultUserAgent = "mudl"

type byteRange struct {
	start int64
	end   int64
}

type task struct {
	id       int64
	start    int64
	next     int64
	reserved int64
	end      int64
}

type scheduler struct {
	total       int64
	workers     int
	minChunk    int64
	maxChunk    int64
	pending     []byteRange
	active      map[*task]struct{}
	mu          sync.Mutex
	nextTaskID  int64
	stealEvents int64
}

func newScheduler(total int64, workers int, minChunk, maxChunk int64) *scheduler {
	return newSchedulerWithPending(total, workers, minChunk, maxChunk, []byteRange{{start: 0, end: total - 1}})
}

func newSchedulerWithPending(total int64, workers int, minChunk, maxChunk int64, pending []byteRange) *scheduler {
	return &scheduler{
		total:    total,
		workers:  workers,
		minChunk: minChunk,
		maxChunk: maxChunk,
		pending:  append([]byteRange(nil), pending...),
		active:   make(map[*task]struct{}),
	}
}

func (s *scheduler) nextTask() *task {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.pending) > 0 {
		r := s.pending[0]
		s.pending = s.pending[1:]
		t := &task{id: s.nextTaskID, start: r.start, next: r.start, reserved: r.start - 1, end: r.end}
		s.nextTaskID++
		s.active[t] = struct{}{}
		return t
	}

	var victim *task
	var biggest int64
	var protectedStart int64
	for t := range s.active {
		start := max64(t.next, t.reserved+1)
		remaining := t.end - start + 1
		if remaining > biggest || (remaining == biggest && victim != nil && t.start > victim.start) {
			biggest = remaining
			victim = t
			protectedStart = start
		}
	}
	if victim == nil || biggest < s.minChunk*2 {
		return nil
	}

	oldEnd := victim.end
	split := protectedStart + biggest/2
	if oldEnd-split+1 < s.minChunk {
		return nil
	}
	victim.end = split - 1
	t := &task{id: s.nextTaskID, start: split, next: split, reserved: split - 1, end: oldEnd}
	s.nextTaskID++
	s.active[t] = struct{}{}
	s.stealEvents++
	return t
}

func (s *scheduler) reserve(t *task, maxBytes int64) (byteRange, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.next > t.end {
		return byteRange{}, false
	}
	start := t.next
	end := min64(t.end, start+maxBytes-1)
	t.next = end + 1
	return byteRange{start: start, end: end}, true
}

func (s *scheduler) beginRead(t *task, maxBytes int64) (byteRange, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.next > t.end {
		return byteRange{}, false
	}
	start := t.next
	end := min64(t.end, start+maxBytes-1)
	t.reserved = end
	return byteRange{start: start, end: end}, true
}

func (s *scheduler) commitRead(t *task, next int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if next > t.next {
		t.next = next
	}
	if t.reserved < t.next-1 {
		t.reserved = t.next - 1
	}
}

func (s *scheduler) releaseRead(t *task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.reserved = t.next - 1
}

func (s *scheduler) taskBounds(t *task) (int64, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return t.next, t.end
}

func (s *scheduler) finish(t *task) {
	s.mu.Lock()
	delete(s.active, t)
	s.mu.Unlock()
}

func (s *scheduler) activeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.active)
}

func (s *scheduler) steals() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stealEvents
}

type workerStat struct {
	active int64
	start  int64
	end    int64
	done   atomic.Int64
	window atomic.Int64
}

func (ws *workerStat) begin(start, end int64) {
	atomic.StoreInt64(&ws.active, 1)
	atomic.StoreInt64(&ws.start, start)
	atomic.StoreInt64(&ws.end, end)
	ws.done.Store(0)
	ws.window.Store(0)
}

func (ws *workerStat) add(n int64) {
	ws.done.Add(n)
	ws.window.Add(n)
}

func (ws *workerStat) idle() {
	atomic.StoreInt64(&ws.active, 0)
}

type probeResult struct {
	size       int64
	ranges     bool
	filename   string
	finalURL   string
	statusCode int
	rangeNote  string
}

func main() {
	var output string
	var workers int
	var minChunkText string
	var maxChunkText string
	var timeoutSec int
	var retries int
	var bufferText string
	var reserveText string
	var userAgent string
	var progressMode string
	var checkOnly bool
	flag.StringVar(&output, "o", "", "output file path")
	flag.IntVar(&workers, "c", 32, "concurrent workers")
	flag.StringVar(&minChunkText, "min-chunk", "4MB", "minimum dynamic range size")
	flag.StringVar(&maxChunkText, "max-chunk", "64MB", "maximum dynamic range size")
	flag.IntVar(&timeoutSec, "timeout", 30, "HTTP response header timeout in seconds")
	flag.IntVar(&retries, "retries", 5, "retries per reserved range")
	flag.StringVar(&bufferText, "buffer", "1MB", "per-read buffer size")
	flag.StringVar(&reserveText, "reserve", "32MB", "bytes reserved per HTTP Range request")
	flag.StringVar(&userAgent, "ua", defaultUserAgent, "HTTP User-Agent")
	flag.StringVar(&progressMode, "progress", "summary", "progress display: summary, details, none")
	flag.BoolVar(&checkOnly, "check", false, "probe URL and exit without downloading")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: mudl_go [options] URL")
		os.Exit(2)
	}
	rawURL := flag.Arg(0)

	minChunk, err := parseSize(minChunkText)
	must(err)
	maxChunk, err := parseSize(maxChunkText)
	must(err)
	bufferSize, err := parseSize(bufferText)
	must(err)
	reserveSize, err := parseSize(reserveText)
	must(err)
	if maxChunk < minChunk {
		maxChunk = minChunk
	}
	if reserveSize < bufferSize {
		reserveSize = bufferSize
	}

	client := makeHTTPClient(time.Duration(timeoutSec)*time.Second, userAgent)
	if checkOnly {
		must(checkURL(client, rawURL, userAgent))
		return
	}
	saved, err := download(context.Background(), client, rawURL, output, workers, minChunk, maxChunk, bufferSize, reserveSize, retries, userAgent, progressMode)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	fmt.Println("Saved to", saved)
}

func checkURL(client *http.Client, rawURL, userAgent string) error {
	headProbe, err := probe(client, rawURL, userAgent)
	if err != nil {
		return err
	}
	rangeProbe, rangeErr := probeRange(client, rawURL, userAgent)
	merged := headProbe
	if rangeErr == nil && rangeProbe.ranges {
		merged = mergeProbe(headProbe, rangeProbe)
	}
	fmt.Println("HEAD status:", headProbe.statusCode)
	fmt.Println("HEAD size:", headProbe.size)
	fmt.Println("HEAD Accept-Ranges:", headProbe.ranges)
	if rangeErr != nil {
		fmt.Println("Range probe error:", rangeErr)
	} else {
		fmt.Println("Range status:", rangeProbe.statusCode)
		fmt.Println("Range supported:", rangeProbe.ranges)
		fmt.Println("Range size:", rangeProbe.size)
	}
	fmt.Println("Filename:", merged.filename)
	fmt.Println("Final URL:", merged.finalURL)
	return nil
}

func download(ctx context.Context, client *http.Client, rawURL, output string, workers int, minChunk, maxChunk, bufferSize, reserveSize int64, retries int, userAgent, progressMode string) (string, error) {
	probe, err := probe(client, rawURL, userAgent)
	if err != nil {
		return "", err
	}
	if !probe.ranges {
		rangeProbe, err := probeRange(client, rawURL, userAgent)
		if err == nil && rangeProbe.ranges {
			probe = mergeProbe(probe, rangeProbe)
		}
	}
	if probe.size <= 0 {
		return "", errors.New("server did not provide Content-Length")
	}

	if output == "" {
		output = probe.filename
	}
	if output == "" {
		output = "download.bin"
	}

	if !probe.ranges {
		fmt.Println("Server did not confirm HTTP Range support; falling back to single connection.")
		return singleDownload(ctx, client, rawURL, output, probe.size, userAgent)
	}

	if probe.rangeNote != "" {
		fmt.Println(probe.rangeNote)
	}

	absOutput, err := filepath.Abs(output)
	if err != nil {
		return "", err
	}

	file, err := os.OpenFile(absOutput, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err := file.Truncate(probe.size); err != nil {
		return "", err
	}

	workers = max(1, min(workers, 128))
	sched := newScheduler(probe.size, workers, minChunk, maxChunk)
	stats := make([]*workerStat, workers)
	for i := range stats {
		stats[i] = &workerStat{}
	}

	var done atomic.Int64
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	progressDone := make(chan struct{})
	go printProgress(progressDone, probe.size, &done, sched, stats, progressMode)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if err := worker(ctx, client, rawURL, file, sched, stats[id], &done, bufferSize, reserveSize, retries, userAgent); err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(progressDone)
	fmt.Println()

	select {
	case err := <-errCh:
		return "", err
	default:
	}
	if done.Load() != probe.size {
		return "", fmt.Errorf("downloaded %d bytes, expected %d", done.Load(), probe.size)
	}
	return absOutput, nil
}

func singleDownload(ctx context.Context, client *http.Client, rawURL, output string, total int64, userAgent string) (string, error) {
	absOutput, err := filepath.Abs(output)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("single connection request failed: %s", resp.Status)
	}
	file, err := os.Create(absOutput)
	if err != nil {
		return "", err
	}
	defer file.Close()

	buf := make([]byte, 1024*1024)
	var done atomic.Int64
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		last := int64(0)
		for {
			select {
			case <-stop:
				current := done.Load()
				fmt.Printf("\rTotal %6.2f%%  %s / %s  %s/s\n",
					float64(current)*100/float64(max64(1, total)), humanSize(current), humanSize(total), humanSize(current-last))
				return
			case <-ticker.C:
				current := done.Load()
				fmt.Printf("\rTotal %6.2f%%  %s / %s  %s/s",
					float64(current)*100/float64(max64(1, total)), humanSize(current), humanSize(total), humanSize(current-last))
				last = current
			}
		}
	}()
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := file.Write(buf[:n]); err != nil {
				close(stop)
				return "", err
			}
			done.Add(int64(n))
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			close(stop)
			return "", readErr
		}
	}
	close(stop)
	return absOutput, nil
}

func mergeProbe(headProbe, rangeProbe probeResult) probeResult {
	if rangeProbe.size > 0 {
		headProbe.size = rangeProbe.size
	}
	if rangeProbe.filename != "" && rangeProbe.filename != "download.bin" {
		headProbe.filename = rangeProbe.filename
	}
	if rangeProbe.finalURL != "" {
		headProbe.finalURL = rangeProbe.finalURL
	}
	headProbe.ranges = rangeProbe.ranges
	headProbe.statusCode = rangeProbe.statusCode
	headProbe.rangeNote = rangeProbe.rangeNote
	return headProbe
}

func worker(ctx context.Context, client *http.Client, rawURL string, file *os.File, sched *scheduler, stat *workerStat, totalDone *atomic.Int64, bufferSize, reserveSize int64, retries int, userAgent string) error {
	buf := make([]byte, bufferSize)
	for {
		t := sched.nextTask()
		if t == nil {
			stat.idle()
			return nil
		}
		stat.begin(t.start, t.end)
		if err := runTask(ctx, client, rawURL, file, sched, t, stat, totalDone, buf, reserveSize, retries, userAgent); err != nil {
			sched.finish(t)
			stat.idle()
			return err
		}
		sched.finish(t)
		stat.idle()
	}
}

func runTask(ctx context.Context, client *http.Client, rawURL string, file *os.File, sched *scheduler, t *task, stat *workerStat, totalDone *atomic.Int64, buf []byte, reserveSize int64, retries int, userAgent string) error {
	_ = reserveSize
	for attempt := 0; attempt <= retries; attempt++ {
		start, end := sched.taskBounds(t)
		if start > end {
			return nil
		}
		var lastErr error
		_, err := downloadDynamicRange(ctx, client, rawURL, file, sched, t, stat, totalDone, buf, userAgent)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == retries {
			return lastErr
		}
		time.Sleep(time.Duration(150*(attempt+1)) * time.Millisecond)
	}
	return nil
}

func downloadRange(ctx context.Context, client *http.Client, rawURL string, file *os.File, r byteRange, buf []byte, userAgent string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", r.start, r.end))

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("server ignored Range request: %s", resp.Status)
	}

	offset := r.start
	left := r.end - r.start + 1
	written := int64(0)
	for left > 0 {
		want := int64(len(buf))
		if left < want {
			want = left
		}
		n, readErr := io.ReadFull(resp.Body, buf[:want])
		if n > 0 {
			if _, err := file.WriteAt(buf[:n], offset); err != nil {
				return written, err
			}
			offset += int64(n)
			left -= int64(n)
			written += int64(n)
		}
		if readErr != nil {
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				return written, fmt.Errorf("incomplete range %d-%d", r.start, r.end)
			}
			return written, readErr
		}
	}
	return written, nil
}

func downloadDynamicRange(ctx context.Context, client *http.Client, rawURL string, file *os.File, sched *scheduler, t *task, stat *workerStat, totalDone *atomic.Int64, buf []byte, userAgent string) (int64, error) {
	first, ok := sched.beginRead(t, int64(len(buf)))
	if !ok {
		return 0, nil
	}
	start := first.start
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		sched.releaseRead(t)
		return 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-", start))

	resp, err := client.Do(req)
	if err != nil {
		sched.releaseRead(t)
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		sched.releaseRead(t)
		return 0, fmt.Errorf("server ignored Range request: %s", resp.Status)
	}

	offset := start
	written := int64(0)
	current := first
	for {
		if offset > current.end {
			next, ok := sched.beginRead(t, int64(len(buf)))
			if !ok {
				return written, nil
			}
			if next.start != offset {
				sched.releaseRead(t)
				return written, fmt.Errorf("dynamic range cursor mismatch: got %d, want %d", next.start, offset)
			}
			current = next
		}
		want := current.end - offset + 1
		n, readErr := io.ReadFull(resp.Body, buf[:want])
		if n > 0 {
			if _, err := file.WriteAt(buf[:n], offset); err != nil {
				sched.releaseRead(t)
				return written, err
			}
			offset += int64(n)
			written += int64(n)
			stat.add(int64(n))
			totalDone.Add(int64(n))
			sched.commitRead(t, offset)
		}
		if readErr != nil {
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				if offset > current.end {
					return written, nil
				}
				sched.releaseRead(t)
				return written, fmt.Errorf("incomplete range from %d", start)
			}
			sched.releaseRead(t)
			return written, readErr
		}
	}
}

func probe(client *http.Client, rawURL, userAgent string) (probeResult, error) {
	req, err := http.NewRequest(http.MethodHead, rawURL, nil)
	if err != nil {
		return probeResult{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return probeResult{}, err
	}
	defer resp.Body.Close()

	size, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	return probeResult{
		size:       size,
		ranges:     strings.EqualFold(resp.Header.Get("Accept-Ranges"), "bytes"),
		filename:   filenameFromResponse(resp.Header, resp.Request.URL.String(), rawURL),
		finalURL:   resp.Request.URL.String(),
		statusCode: resp.StatusCode,
	}, nil
}

func probeRange(client *http.Client, rawURL, userAgent string) (probeResult, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return probeResult{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Range", "bytes=0-0")
	resp, err := client.Do(req)
	if err != nil {
		return probeResult{}, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	size := int64(0)
	if contentRange := resp.Header.Get("Content-Range"); contentRange != "" {
		if parsed, ok := parseContentRangeSize(contentRange); ok {
			size = parsed
		}
	}
	if size == 0 {
		size, _ = strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	}
	ranges := resp.StatusCode == http.StatusPartialContent
	note := ""
	if ranges {
		note = "HTTP Range confirmed by GET bytes=0-0."
	}
	return probeResult{
		size:       size,
		ranges:     ranges,
		filename:   filenameFromResponse(resp.Header, resp.Request.URL.String(), rawURL),
		finalURL:   resp.Request.URL.String(),
		statusCode: resp.StatusCode,
		rangeNote:  note,
	}, nil
}

func parseContentRangeSize(value string) (int64, bool) {
	slash := strings.LastIndex(value, "/")
	if slash < 0 || slash == len(value)-1 {
		return 0, false
	}
	sizeText := strings.TrimSpace(value[slash+1:])
	if sizeText == "*" {
		return 0, false
	}
	size, err := strconv.ParseInt(sizeText, 10, 64)
	if err != nil || size <= 0 {
		return 0, false
	}
	return size, true
}

func filenameFromResponse(headers http.Header, finalURL, originalURL string) string {
	if cd := headers.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			if name := params["filename"]; name != "" {
				return sanitizeFilename(name)
			}
		}
	}
	if parsed, err := url.Parse(finalURL); err == nil {
		q := parsed.Query()
		for _, key := range []string{"fileName", "filename", "name"} {
			if value := q.Get(key); value != "" {
				return sanitizeFilename(value)
			}
		}
		if base := path.Base(parsed.Path); base != "." && base != "/" {
			return sanitizeFilename(base)
		}
	}
	if parsed, err := url.Parse(originalURL); err == nil {
		if base := path.Base(parsed.Path); base != "." && base != "/" {
			return sanitizeFilename(base)
		}
	}
	return "download.bin"
}

func sanitizeFilename(name string) string {
	name = strings.Trim(strings.TrimSpace(name), "\"'")
	re := regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)
	name = re.ReplaceAllString(name, "_")
	name = strings.TrimRight(name, " .")
	if name == "" {
		return "download.bin"
	}
	return name
}

func printProgress(doneCh <-chan struct{}, total int64, done *atomic.Int64, sched *scheduler, stats []*workerStat, mode string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastDone := int64(0)
	smoothedSpeed := float64(0)
	first := true
	previous := 0
	for {
		select {
		case <-doneCh:
			current := done.Load()
			instant := current - lastDone
			if smoothedSpeed == 0 {
				smoothedSpeed = float64(instant)
			}
			renderProgress(total, current, int64(smoothedSpeed), sched, stats, first, previous, mode)
			if mode == "summary" {
				fmt.Println()
			}
			return
		case <-ticker.C:
			current := done.Load()
			instant := current - lastDone
			if first {
				smoothedSpeed = float64(instant)
			} else {
				smoothedSpeed = smoothedSpeed*0.7 + float64(instant)*0.3
			}
			previous = renderProgress(total, current, int64(smoothedSpeed), sched, stats, first, previous, mode)
			lastDone = current
			first = false
		}
	}
}

func renderProgress(total, current, speed int64, sched *scheduler, stats []*workerStat, first bool, previous int, mode string) int {
	if mode == "none" {
		return 0
	}
	percent := float64(current) * 100 / float64(total)
	if mode != "details" {
		line := fmt.Sprintf("%6.2f%%  %s/%s  %s/s  conn %d/%d  split %d",
			percent, humanSize(current), humanSize(total), humanSize(speed), sched.activeCount(), len(stats), sched.steals())
		clear := 0
		if previous > len(line) {
			clear = previous - len(line)
		}
		fmt.Printf("\r%s%s", line, strings.Repeat(" ", clear))
		return len(line)
	}
	if !first && previous > 0 {
		fmt.Printf("\033[%dF", previous)
	}
	output := []string{
		fmt.Sprintf("Total %6.2f%%  %s / %s  %s/s  active %d/%d  steals %d",
			percent, humanSize(current), humanSize(total), humanSize(speed), sched.activeCount(), len(stats), sched.steals()),
	}
	for i, st := range stats {
		if atomic.LoadInt64(&st.active) == 0 {
			output = append(output, fmt.Sprintf("T%02d  idle", i+1))
			continue
		}
		window := st.window.Swap(0)
		start := atomic.LoadInt64(&st.start)
		end := atomic.LoadInt64(&st.end)
		doneInTask := st.done.Load()
		size := max64(1, end-start+1)
		output = append(output, fmt.Sprintf("T%02d  %s/s  %6.2f%%  bytes %d-%d",
			i+1, humanSize(window), float64(doneInTask)*100/float64(size), start, end))
	}
	for _, line := range output {
		if len(line) > 120 {
			line = line[:120]
		}
		fmt.Printf("%-120s\n", line)
	}
	return len(output)
}

func makeHTTPClient(timeout time.Duration, userAgent string) *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Header.Del("Referer")
			req.Header.Set("User-Agent", userAgent)
			if len(via) > 0 {
				if rangeHeader := via[0].Header.Get("Range"); rangeHeader != "" {
					req.Header.Set("Range", rangeHeader)
				}
				if encoding := via[0].Header.Get("Accept-Encoding"); encoding != "" {
					req.Header.Set("Accept-Encoding", encoding)
				}
			}
			return nil
		},
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          256,
			MaxIdleConnsPerHost:   128,
			MaxConnsPerHost:       128,
			IdleConnTimeout:       90 * time.Second,
			ForceAttemptHTTP2:     true,
			ResponseHeaderTimeout: timeout,
		},
	}
}

func parseSize(text string) (int64, error) {
	re := regexp.MustCompile(`(?i)^\s*(\d+(?:\.\d+)?)\s*([kmgt]?b?)?\s*$`)
	match := re.FindStringSubmatch(text)
	if match == nil {
		return 0, fmt.Errorf("invalid size: %s", text)
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, err
	}
	unit := strings.ToLower(match[2])
	mul := float64(1)
	switch unit {
	case "", "b":
	case "k", "kb":
		mul = 1024
	case "m", "mb":
		mul = 1024 * 1024
	case "g", "gb":
		mul = 1024 * 1024 * 1024
	case "t", "tb":
		mul = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("invalid size unit: %s", unit)
	}
	return int64(value * mul), nil
}

func humanSize(value int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(value)
	for _, unit := range units {
		if v < 1024 || unit == "TB" {
			if unit == "B" {
				return fmt.Sprintf("%d B", value)
			}
			return fmt.Sprintf("%.1f %s", v, unit)
		}
		v /= 1024
	}
	return fmt.Sprintf("%d B", value)
}

func clamp(value, low, high int64) int64 {
	return min64(max64(value, low), high)
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

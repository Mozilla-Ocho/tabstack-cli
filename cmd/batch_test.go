package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withStdin points the "-" URL list at a string for the duration of a test.
func withStdin(t *testing.T, content string) {
	t.Helper()
	prev := stdinReader
	stdinReader = strings.NewReader(content)
	t.Cleanup(func() { stdinReader = prev })
}

func TestReadURLList(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"plain list", "https://a.com\nhttps://b.com\n", []string{"https://a.com", "https://b.com"}},
		{"blank lines skipped", "https://a.com\n\n\nhttps://b.com\n", []string{"https://a.com", "https://b.com"}},
		{"whitespace trimmed", "  https://a.com  \n\thttps://b.com\t\n", []string{"https://a.com", "https://b.com"}},
		{"comments skipped", "# a list\nhttps://a.com\n# note\nhttps://b.com\n", []string{"https://a.com", "https://b.com"}},
		{"no trailing newline", "https://a.com", []string{"https://a.com"}},
		{"empty", "", nil},
		{"only comments and blanks", "# nothing\n\n", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readURLList(strings.NewReader(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveURLs(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		stdin      string
		stdinTaken string
		want       []string
		wantCode   int
	}{
		{
			name: "literal URLs pass through",
			args: []string{"https://a.com", "https://b.com"},
			want: []string{"https://a.com", "https://b.com"},
		},
		{
			name:  "a dash expands the stdin list",
			args:  []string{"-"},
			stdin: "https://a.com\nhttps://b.com\n",
			want:  []string{"https://a.com", "https://b.com"},
		},
		{
			name:  "a dash expands in place among literals",
			args:  []string{"https://first.com", "-", "https://last.com"},
			stdin: "https://mid.com\n",
			want:  []string{"https://first.com", "https://mid.com", "https://last.com"},
		},
		{
			name: "duplicates are dropped, first position kept",
			args: []string{"https://a.com", "https://b.com", "https://a.com"},
			want: []string{"https://a.com", "https://b.com"},
		},
		{
			name:     "a dash twice is a usage error",
			args:     []string{"-", "-"},
			stdin:    "https://a.com\n",
			wantCode: 2,
		},
		{
			name:       "competing for stdin is a usage error",
			args:       []string{"-"},
			stdinTaken: "--schema",
			wantCode:   2,
		},
		{
			name:     "empty stdin is a usage error",
			args:     []string{"-"},
			stdin:    "\n\n# nothing\n",
			wantCode: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withStdin(t, tc.stdin)
			got, err := resolveURLs(tc.args, tc.stdinTaken)

			if tc.wantCode != 0 {
				if codeOf(err) != tc.wantCode {
					t.Fatalf("exit code = %d, want %d (err: %v)", codeOf(err), tc.wantCode, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveURLsConflictNamesBothSides: the message has to say which flag is
// competing, or the user cannot tell what to change.
func TestResolveURLsConflictNamesBothSides(t *testing.T) {
	withStdin(t, "")
	_, err := resolveURLs([]string{"-"}, "--schema")
	if err == nil {
		t.Fatal("expected a conflict error")
	}
	for _, want := range []string{"--schema", "stdin"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %q: %v", want, err)
		}
	}
}

func TestValidateURLs(t *testing.T) {
	if err := validateURLs([]string{"https://a.com", "http://b.com"}); err != nil {
		t.Errorf("valid URLs rejected: %v", err)
	}
	err := validateURLs([]string{"https://a.com", "not-a-url", "https://c.com"})
	if codeOf(err) != 2 {
		t.Errorf("exit code = %d, want 2", codeOf(err))
	}
}

func TestCheckRawBatch(t *testing.T) {
	cases := []struct {
		name      string
		raw       bool
		count     int
		outputDir string
		wantErr   bool
	}{
		{"raw with one URL is fine", true, 1, "", false},
		{"raw with several URLs on stdout is refused", true, 3, "", true},
		{"raw with several URLs into a directory is fine", true, 3, "/tmp/x", false},
		{"no raw, several URLs", false, 3, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkRawBatch(tc.raw, tc.count, tc.outputDir)
			if tc.wantErr {
				if codeOf(err) != 2 {
					t.Errorf("exit code = %d, want 2", codeOf(err))
				}
				if err != nil && !strings.Contains(err.Error(), "--output-dir") {
					t.Errorf("message should point at the fix: %v", err)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestOutputFileName(t *testing.T) {
	cases := []struct {
		name       string
		url        string
		ext        string
		wantPrefix string
	}{
		{"host and path", "https://example.com/blog/post-1", ".md", "example.com_blog_post-1-"},
		{"bare host", "https://other.org", ".md", "other.org-"},
		{"query is not in the readable part", "https://example.com/s?q=a", ".json", "example.com_s-"},
		{"unsafe characters collapse", "https://ex.com/a b/c:d", ".md", "ex.com_a-b_c-d-"},
		{"uppercase is folded", "https://EXAMPLE.com/Path", ".md", "example.com_path-"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := outputFileName(tc.url, tc.ext)
			if !strings.HasPrefix(got, tc.wantPrefix) {
				t.Errorf("got %q, want prefix %q", got, tc.wantPrefix)
			}
			if !strings.HasSuffix(got, tc.ext) {
				t.Errorf("got %q, want suffix %q", got, tc.ext)
			}
		})
	}

	t.Run("stable for the same URL", func(t *testing.T) {
		a := outputFileName("https://example.com/x", ".md")
		b := outputFileName("https://example.com/x", ".md")
		if a != b {
			t.Errorf("%q != %q, names must be a pure function of the URL", a, b)
		}
	})

	t.Run("URLs differing only in query get different names", func(t *testing.T) {
		a := outputFileName("https://example.com/s?q=a", ".md")
		b := outputFileName("https://example.com/s?q=b", ".md")
		if a == b {
			t.Errorf("both produced %q, so one would overwrite the other", a)
		}
	})

	t.Run("a very long path is capped", func(t *testing.T) {
		got := outputFileName("https://example.com/"+strings.Repeat("segment/", 60), ".md")
		if len(got) > maxOutputNameBase+20 {
			t.Errorf("name is %d chars: %q", len(got), got)
		}
	})
}

func TestWriteOutputFile(t *testing.T) {
	dir := t.TempDir()
	const u = "https://example.com/a"

	path, err := writeOutputFile(dir, u, ".md", []byte("# hi"), false)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "# hi\n" {
		t.Errorf("content = %q, want exactly one trailing newline", body)
	}

	t.Run("an existing file is refused", func(t *testing.T) {
		if _, err := writeOutputFile(dir, u, ".md", []byte("new"), false); err == nil {
			t.Fatal("expected a refusal")
		} else if !strings.Contains(err.Error(), "--force") {
			t.Errorf("message should name the escape hatch: %v", err)
		}
	})

	t.Run("force overwrites", func(t *testing.T) {
		if _, err := writeOutputFile(dir, u, ".md", []byte("new"), true); err != nil {
			t.Fatal(err)
		}
		body, _ := os.ReadFile(filepath.Join(dir, outputFileName(u, ".md")))
		if string(body) != "new\n" {
			t.Errorf("content = %q", body)
		}
	})
}

// TestRunBatchEmitsInInputOrder is the ordering guarantee. Completion order is
// deliberately scrambled: without the sliding window the output would follow it.
func TestRunBatchEmitsInInputOrder(t *testing.T) {
	urls := []string{"u0", "u1", "u2", "u3", "u4"}
	// u0 is slowest, so completion order is the reverse of input order.
	delays := map[string]time.Duration{
		"u0": 120 * time.Millisecond,
		"u1": 90 * time.Millisecond,
		"u2": 60 * time.Millisecond,
		"u3": 30 * time.Millisecond,
		"u4": 0,
	}

	var got []string
	items := runBatch(context.Background(), urls, 5,
		func(_ context.Context, target string) (json.RawMessage, []byte, error) {
			time.Sleep(delays[target])
			return json.RawMessage(`{}`), []byte(target), nil
		},
		nil,
		func(item batchItem) { got = append(got, item.URL) },
	)

	if !reflect.DeepEqual(got, urls) {
		t.Errorf("emitted %q, want input order %q", got, urls)
	}
	if len(items) != len(urls) {
		t.Errorf("got %d items, want %d", len(items), len(urls))
	}
}

// TestRunBatchRespectsConcurrency checks the semaphore actually bounds how many
// requests are in flight, since exceeding it is exactly what trips a rate limit.
func TestRunBatchRespectsConcurrency(t *testing.T) {
	const limit = 3
	var inFlight, peak atomic.Int32

	urls := make([]string, 20)
	for i := range urls {
		urls[i] = fmt.Sprintf("u%d", i)
	}

	runBatch(context.Background(), urls, limit,
		func(_ context.Context, _ string) (json.RawMessage, []byte, error) {
			cur := inFlight.Add(1)
			for {
				old := peak.Load()
				if cur <= old || peak.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			inFlight.Add(-1)
			return json.RawMessage(`{}`), nil, nil
		},
		nil,
		func(batchItem) {},
	)

	if peak.Load() > limit {
		t.Errorf("peak concurrency was %d, want at most %d", peak.Load(), limit)
	}
	if peak.Load() < 2 {
		t.Errorf("peak concurrency was %d, so nothing ran in parallel", peak.Load())
	}
}

// TestRunBatchRecordsFailures: a failing URL must not abort the rest, and its
// per-URL code should match what the same failure would exit with alone.
func TestRunBatchRecordsFailures(t *testing.T) {
	urls := []string{"ok1", "bad", "ok2"}
	items := runBatch(context.Background(), urls, 2,
		func(_ context.Context, target string) (json.RawMessage, []byte, error) {
			if target == "bad" {
				return nil, nil, errors.New("boom")
			}
			return json.RawMessage(`{}`), nil, nil
		},
		nil,
		func(batchItem) {},
	)

	if len(items) != 3 {
		t.Fatalf("got %d items", len(items))
	}
	if !items[0].OK || !items[2].OK {
		t.Error("a failure stopped the other URLs")
	}
	if items[1].OK {
		t.Error("the failing URL was recorded as ok")
	}
	if items[1].Error == nil || items[1].Error.Code != 1 {
		t.Errorf("error = %+v, want code 1", items[1].Error)
	}
}

// TestRunBatchCancellation: Ctrl-C mid-batch must stop promptly and still
// account for every URL, so the summary is not silently short.
func TestRunBatchCancellation(t *testing.T) {
	urls := make([]string, 40)
	for i := range urls {
		urls[i] = fmt.Sprintf("u%d", i)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var started atomic.Int32
	var once sync.Once

	start := time.Now()
	items := runBatch(ctx, urls, 2,
		func(ctx context.Context, _ string) (json.RawMessage, []byte, error) {
			if started.Add(1) >= 3 {
				once.Do(cancel)
			}
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(20 * time.Millisecond):
				return json.RawMessage(`{}`), nil, nil
			}
		},
		nil,
		func(batchItem) {},
	)

	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("took %v, want a prompt stop", elapsed)
	}
	if len(items) != len(urls) {
		t.Errorf("got %d items, want one per URL (%d) so the summary is complete", len(items), len(urls))
	}
}

func TestBatchOutcome(t *testing.T) {
	cases := []struct {
		name     string
		items    []batchItem
		wantCode int
	}{
		{
			name:     "all succeeded",
			items:    []batchItem{{URL: "a", OK: true}, {URL: "b", OK: true}},
			wantCode: 0,
		},
		{
			name:     "one failed",
			items:    []batchItem{{URL: "a", OK: true}, {URL: "b", Error: &batchError{Code: 3, Message: "nope"}}},
			wantCode: 3,
		},
		{
			name:     "all failed",
			items:    []batchItem{{URL: "a", Error: &batchError{Code: 1, Message: "x"}}},
			wantCode: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			setTestApp(t)
			err := batchOutcome(rootApp.renderer, tc.items)
			if tc.wantCode == 0 {
				if err != nil {
					t.Errorf("err = %v, want nil", err)
				}
				return
			}
			if codeOf(err) != tc.wantCode {
				t.Errorf("exit code = %d, want %d", codeOf(err), tc.wantCode)
			}
		})
	}
}

package client

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseDeprecation(t *testing.T) {
	sunset := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		hdr  http.Header
		ok   bool
		want Deprecation
	}{
		{name: "none", hdr: http.Header{}, ok: false},
		{
			name: "rfc 9745 date with sunset and link",
			hdr: http.Header{
				"Deprecation": {"@1767225600"},
				"Sunset":      {sunset.Format(http.TimeFormat)},
				"Link":        {`<https://docs.example/next>; rel="successor-version", <https://docs.example/dep>; rel="deprecation"`},
			},
			ok:   true,
			want: Deprecation{Since: time.Unix(1767225600, 0), Sunset: sunset, Link: "https://docs.example/dep"},
		},
		{
			name: "legacy true form",
			hdr:  http.Header{"Deprecation": {"true"}},
			ok:   true,
			want: Deprecation{},
		},
		{
			name: "legacy http-date form",
			hdr:  http.Header{"Deprecation": {sunset.Format(http.TimeFormat)}},
			ok:   true,
			want: Deprecation{Since: sunset},
		},
		{
			name: "sunset only, sunset link fallback",
			hdr: http.Header{
				"Sunset": {sunset.Format(http.TimeFormat)},
				"Link":   {`<https://docs.example/sunset>; rel="sunset"`},
			},
			ok:   true,
			want: Deprecation{Sunset: sunset, Link: "https://docs.example/sunset"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseDeprecation(tc.hdr)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !got.Since.Equal(tc.want.Since) || !got.Sunset.Equal(tc.want.Sunset) || got.Link != tc.want.Link {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDeprecationNotifiedOncePerEndpoint(t *testing.T) {
	var calls atomic.Int32
	var got []Deprecation
	c, _, _ := retryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Deprecation", "@1767225600")
		// First call is rate limited, so the warning must not repeat on retry.
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"content":"ok","url":"u"}`)
	}, WithDeprecationNotify(func(d Deprecation) { got = append(got, d) }))

	for range 2 {
		if _, err := c.ExtractMarkdown(context.Background(), ExtractMarkdownRequest{URL: "https://e"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 1 {
		t.Fatalf("notified %d times, want 1", len(got))
	}
	if got[0].Method != http.MethodPost || got[0].Endpoint != "/extract/markdown" {
		t.Errorf("got %+v", got[0])
	}
}

func TestNoDeprecationNoNotify(t *testing.T) {
	notified := false
	c, _, _ := retryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"content":"ok","url":"u"}`)
	}, WithDeprecationNotify(func(Deprecation) { notified = true }))
	if _, err := c.ExtractMarkdown(context.Background(), ExtractMarkdownRequest{URL: "https://e"}); err != nil {
		t.Fatal(err)
	}
	if notified {
		t.Error("notified without deprecation headers")
	}
}

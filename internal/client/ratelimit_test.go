package client

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRateLimit(t *testing.T) {
	now := time.Unix(1000, 0)
	cases := []struct {
		name string
		hdr  map[string]string
		want RateLimit
	}{
		{
			name: "none",
			hdr:  nil,
			want: RateLimit{},
		},
		{
			name: "structured fields",
			hdr: map[string]string{
				"RateLimit":        `"default";r=50;t=30, "burst";r=5;t=1`,
				"RateLimit-Policy": `"default";q=100;w=60`,
			},
			want: RateLimit{Limit: "100", Remaining: "50", Reset: now.Add(30 * time.Second)},
		},
		{
			name: "earlier draft",
			hdr: map[string]string{
				"RateLimit-Limit":     "100, 100;w=60",
				"RateLimit-Remaining": "42",
				"RateLimit-Reset":     "15",
			},
			want: RateLimit{Limit: "100", Remaining: "42", Reset: now.Add(15 * time.Second)},
		},
		{
			name: "x-ratelimit unix reset",
			hdr: map[string]string{
				"X-RateLimit-Limit":     "1000",
				"X-RateLimit-Remaining": "997",
				"X-RateLimit-Reset":     "1785348780",
			},
			want: RateLimit{Limit: "1000", Remaining: "997", Reset: time.Unix(1785348780, 0)},
		},
		{
			name: "x-ratelimit delta reset",
			hdr:  map[string]string{"X-RateLimit-Reset": "20"},
			want: RateLimit{Reset: now.Add(20 * time.Second)},
		},
		{
			name: "structured wins over legacy per field",
			hdr: map[string]string{
				"RateLimit":         `"default";r=1;t=2`,
				"X-RateLimit-Limit": "500",
				"X-RateLimit-Reset": "1785348780",
			},
			want: RateLimit{Limit: "500", Remaining: "1", Reset: now.Add(2 * time.Second)},
		},
		{
			name: "malformed values ignored",
			hdr: map[string]string{
				"RateLimit":       `"default";t=soon`,
				"RateLimit-Reset": "-5",
			},
			want: RateLimit{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range tc.hdr {
				h.Set(k, v)
			}
			got := ParseRateLimit(h, now)
			if got.Limit != tc.want.Limit || got.Remaining != tc.want.Remaining || !got.Reset.Equal(tc.want.Reset) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

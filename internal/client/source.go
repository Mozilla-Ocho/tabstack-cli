package client

import (
	"net/http"
	"strconv"
	"time"
)

// Source is the provenance of the document a fetch-based response was
// extracted from, read from the X-Tabstack-* response headers. It ties an
// extraction to the exact bytes that produced it. Every field is optional: the
// API omits a header when it does not know the value, and older API versions
// send none of them.
type Source struct {
	FinalURL     string    // X-Tabstack-Final-Url: URL after redirects
	OriginStatus int       // X-Tabstack-Origin-Status; 0 if absent
	RetrievedAt  time.Time // X-Tabstack-Retrieved-At; zero if absent
	Checksum     string    // X-Tabstack-Checksum: "sha256:<hex>" of the source document
	Cache        string    // X-Tabstack-Cache: hit, fetch-hit, miss or bypass
	Age          time.Duration
	HasAge       bool   // Age is only sent when cached content was served
	TraceID      string // X-Trace-Id, the id to quote in a support request
}

// Empty reports whether the response carried no provenance at all.
func (s Source) Empty() bool {
	return s == Source{}
}

// sourceFrom reads provenance from response headers, ignoring any header whose
// value does not parse rather than failing the call over metadata.
func sourceFrom(h http.Header) Source {
	s := Source{
		FinalURL: h.Get("X-Tabstack-Final-Url"),
		Checksum: h.Get("X-Tabstack-Checksum"),
		Cache:    h.Get("X-Tabstack-Cache"),
		TraceID:  h.Get("X-Trace-Id"),
	}
	if v, err := strconv.Atoi(h.Get("X-Tabstack-Origin-Status")); err == nil && v > 0 {
		s.OriginStatus = v
	}
	if v, err := time.Parse(time.RFC3339, h.Get("X-Tabstack-Retrieved-At")); err == nil {
		s.RetrievedAt = v
	}
	if v, err := strconv.ParseInt(h.Get("Age"), 10, 64); err == nil && v >= 0 {
		s.Age, s.HasAge = time.Duration(v)*time.Second, true
	}
	return s
}

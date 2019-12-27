package promtest

import (
	"bufio"
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/prometheus/util/promlint"
)

// Collect sets up a temporary HTTP server and performs a single scrape of the
// metrics provided by c. The metrics data is returned as a byte slice for
// further inspection by other functions in this package.
func Collect(t *testing.T, c prometheus.Collector) []byte {
	t.Helper()

	r := prometheus.NewPedanticRegistry()
	r.MustRegister(c)
	h := promhttp.HandlerFor(r, promhttp.HandlerOpts{})

	s := httptest.NewServer(h)
	defer s.Close()

	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatalf("failed to parse temporary HTTP server URL: %v", err)
	}

	client := &http.Client{Timeout: 1 * time.Second}
	res, err := client.Get(u.String())
	if err != nil {
		t.Fatalf("failed to perform HTTP request: %v", err)
	}
	defer res.Body.Close()

	// Set a sane upper limit on the number of bytes in the response body.
	const mebibyte = 1 << 20
	b, err := ioutil.ReadAll(io.LimitReader(res.Body, 16*mebibyte))
	if err != nil {
		t.Fatalf("failed to read HTTP response body: %v", err)
	}

	return b
}

// Lint reports whether the Prometheus metrics in body comply with promlint
// best practices. It returns false if any issues were found.
func Lint(t *testing.T, body []byte) bool {
	t.Helper()

	// Ensure best practices are followed by linting the metrics.
	problems, err := promlint.New(bytes.NewReader(body)).Lint()
	if err != nil {
		t.Fatalf("failed to lint Prometheus metrics: %v", err)
	}

	if len(problems) == 0 {
		return true
	}

	for _, p := range problems {
		t.Logf("lint: %s: %s", p.Metric, p.Text)
	}

	return false
}

// Match reports whether each Prometheus metric in body (ignoring metadata such
// as HELP and TYPE) matches a metric from the input metrics slice.
func Match(t *testing.T, body []byte, metrics []string) bool {
	ok := true
	s := bufio.NewScanner(bytes.NewReader(body))
	for s.Scan() {
		text := s.Text()

		// Skip metadata lines (HELP and TYPE).
		if strings.HasPrefix(text, "#") {
			continue
		}

		var found bool
		for _, m := range metrics {
			if text == m {
				found = true
				break
			}
		}

		if !found {
			ok = false
			t.Logf("Prometheus metric was not found in list: %s", text)
		}
	}

	if err := s.Err(); err != nil {
		t.Fatalf("failed to scan for matching Prometheus metrics: %v", err)
	}

	return ok
}

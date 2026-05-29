package parser_test

import (
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/parser"
)

// mustParse is a test helper that fails the test on any parse error.
func mustParse(t *testing.T, src string) *ast.Query {
	t.Helper()
	q, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q) unexpected error: %v", src, err)
	}
	return q
}

// mustParseErr asserts that parsing src produces an error containing substr.
func mustParseErr(t *testing.T, src, substr string) {
	t.Helper()
	_, err := parser.Parse(src)
	if err == nil {
		t.Fatalf("Parse(%q) expected error, got nil", src)
	}
	if substr != "" && !containsStr(err.Error(), substr) {
		t.Fatalf("Parse(%q) error %q does not contain %q", src, err.Error(), substr)
	}
}

func containsStr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}

// relLookback extracts the Duration from a RelativeLookback time window.
func relLookback(t *testing.T, q *ast.Query) time.Duration {
	t.Helper()
	rl, ok := q.Source.TimeWindow.(*ast.RelativeLookback)
	if !ok {
		t.Fatalf("expected RelativeLookback, got %T", q.Source.TimeWindow)
	}
	return rl.Duration.Duration
}

// ─── last <timespan> tests ────────────────────────────────────────────────────

func TestTimespanSimpleUnits(t *testing.T) {
	cases := []struct {
		src  string
		want time.Duration
	}{
		// Simple integer + single-letter unit
		{"events(last 7d)", 7 * 24 * time.Hour},
		{"events(last 24h)", 24 * time.Hour},
		{"events(last 30m)", 30 * time.Minute},
		{"events(last 4s)", 4 * time.Second},
		{"events(last 2w)", 2 * 7 * 24 * time.Hour},
		// Float magnitude
		{"events(last 2.5h)", time.Duration(2.5 * float64(time.Hour))},
		{"events(last 0.5d)", time.Duration(0.5 * 24 * float64(time.Hour))},
		// Multi-character Go units
		{"events(last 500ms)", 500 * time.Millisecond},
		{"events(last 100us)", 100 * time.Microsecond},
		{"events(last 1000ns)", 1000 * time.Nanosecond},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			q := mustParse(t, tc.src)
			got := relLookback(t, q)
			if got != tc.want {
				t.Errorf("duration = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTimespanCompoundForms(t *testing.T) {
	cases := []struct {
		src  string
		want time.Duration
	}{
		// days + Go duration tail
		{"events(last 1d12h30m)", 24*time.Hour + 12*time.Hour + 30*time.Minute},
		{"events(last 1d12h)", 24*time.Hour + 12*time.Hour},
		// hours + tail
		{"events(last 1h10m10s)", time.Hour + 10*time.Minute + 10*time.Second},
		{"events(last 1h30m)", time.Hour + 30*time.Minute},
		// minutes + tail
		{"events(last 2m30s)", 2*time.Minute + 30*time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			q := mustParse(t, tc.src)
			got := relLookback(t, q)
			if got != tc.want {
				t.Errorf("duration = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTimespanErrors(t *testing.T) {
	cases := []struct {
		src    string
		errSub string
	}{
		// Missing magnitude
		{`events(last d)`, "numeric magnitude"},
		// Unknown unit letter
		{`events(last 7x)`, "unknown timespan unit"},
		// Bad compound tail
		{`events(last 1dXXX)`, "invalid timespan"},
		// Negative magnitude — lexer produces unary minus + int, not parsed as timespan
		{`events(last -7d)`, "numeric magnitude"},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			mustParseErr(t, tc.src, tc.errSub)
		})
	}
}

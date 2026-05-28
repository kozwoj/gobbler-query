package lexer_test

import (
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/lexer"
)

// types extracts the token types from Tokenize output, stopping before EOF.
func types(src string) []lexer.TokenType {
	toks := lexer.Tokenize(src)
	out := make([]lexer.TokenType, 0, len(toks))
	for _, t := range toks {
		if t.Type == lexer.TokenEOF {
			break
		}
		out = append(out, t.Type)
	}
	return out
}

// ── Keywords ──────────────────────────────────────────────────────────────────

func TestKeywords(t *testing.T) {
	cases := []struct {
		src  string
		want lexer.TokenType
	}{
		{"where", lexer.TokenWhere},
		{"project", lexer.TokenProject},
		{"summarize", lexer.TokenSummarize},
		{"by", lexer.TokenBy},
		{"join", lexer.TokenJoin},
		{"on", lexer.TokenOn},
		{"sort", lexer.TokenSort},
		{"asc", lexer.TokenAsc},
		{"desc", lexer.TokenDesc},
		{"take", lexer.TokenTake},
		{"count", lexer.TokenCount},
		{"or", lexer.TokenOr},
		{"and", lexer.TokenAnd},
		{"not", lexer.TokenNot},
		{"in", lexer.TokenIn},
		{"between", lexer.TokenBetween},
		{"isnull", lexer.TokenIsnull},
		{"isnotnull", lexer.TokenIsnotnull},
		{"isempty", lexer.TokenIsempty},
		{"true", lexer.TokenTrue},
		{"false", lexer.TokenFalse},
		{"ago", lexer.TokenAgo},
		{"last", lexer.TokenLast},
		{"sum", lexer.TokenSum},
		{"avg", lexer.TokenAvg},
		{"min", lexer.TokenMin},
		{"max", lexer.TokenMax},
		{"dcount", lexer.TokenDcount},
		{"contains", lexer.TokenContains},
		{"startswith", lexer.TokenStartswith},
		{"endswith", lexer.TokenEndswith},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			got := types(tc.src)
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("Tokenize(%q) types = %v, want [%v]", tc.src, got, tc.want)
			}
		})
	}
}

// ── Identifiers ───────────────────────────────────────────────────────────────

func TestIdentifier_Plain(t *testing.T) {
	for _, src := range []string{"Logs", "statusCode", "_private", "x"} {
		t.Run(src, func(t *testing.T) {
			toks := lexer.Tokenize(src)
			if toks[0].Type != lexer.TokenIdent || toks[0].StrVal != src {
				t.Errorf("got %v", toks[0])
			}
		})
	}
}

func TestIdentifier_Hyphenated(t *testing.T) {
	// Gobbler item-type names: hyphen allowed when followed by a letter.
	cases := []string{"alpha-folder", "my-type-name", "gobbler-ingest-event"}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			toks := lexer.Tokenize(src)
			if toks[0].Type != lexer.TokenIdent || toks[0].StrVal != src {
				t.Errorf("expected single IDENT(%s), got %v", src, toks[0])
			}
		})
	}
}

func TestIdentifier_HyphenBeforeDigit_IsOperator(t *testing.T) {
	// "x-1" and "x - 1" must both produce IDENT MINUS INT, not one identifier.
	for _, src := range []string{"x-1", "x - 1"} {
		t.Run(src, func(t *testing.T) {
			got := types(src)
			want := []lexer.TokenType{lexer.TokenIdent, lexer.TokenMinus, lexer.TokenInt}
			if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
				t.Errorf("Tokenize(%q) types = %v, want %v", src, got, want)
			}
		})
	}
}

// ── Numbers ───────────────────────────────────────────────────────────────────

func TestIntegers(t *testing.T) {
	cases := []struct {
		src  string
		want int64
	}{
		{"0", 0},
		{"42", 42},
		{"4096", 4096},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			toks := lexer.Tokenize(tc.src)
			if toks[0].Type != lexer.TokenInt {
				t.Fatalf("want TokenInt, got %v", toks[0])
			}
			if toks[0].IntVal != tc.want {
				t.Errorf("IntVal = %d, want %d", toks[0].IntVal, tc.want)
			}
		})
	}
}

func TestFloat(t *testing.T) {
	toks := lexer.Tokenize("3.14")
	if toks[0].Type != lexer.TokenFloat || toks[0].FloatVal != 3.14 {
		t.Errorf("got %v", toks[0])
	}
}

func TestRangeSeparator_NotFloat(t *testing.T) {
	// "100..500" must produce INT DOTDOT INT, not a float followed by INT.
	got := types("100..500")
	want := []lexer.TokenType{lexer.TokenInt, lexer.TokenDotDot, lexer.TokenInt}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("got %v, want %v", got, want)
	}
}

// ── Strings ───────────────────────────────────────────────────────────────────

func TestStrings(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`"hello"`, "hello"},
		{`"with spaces"`, "with spaces"},
		{`"tab:\t"`, "tab:\t"},
		{`"newline:\n"`, "newline:\n"},
		{`"backslash:\\"`, `backslash:\`},
		{`"quote:\""`, `quote:"`},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			toks := lexer.Tokenize(tc.src)
			if toks[0].Type != lexer.TokenString {
				t.Fatalf("want TokenString, got %v", toks[0])
			}
			if toks[0].StrVal != tc.want {
				t.Errorf("StrVal = %q, want %q", toks[0].StrVal, tc.want)
			}
		})
	}
}

// ── Datetime literals ─────────────────────────────────────────────────────────

func TestDatetimeLiteral(t *testing.T) {
	cases := []struct {
		src  string
		want time.Time
	}{
		{
			"datetime(2026-01-15)",
			time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			"datetime(2026-01-15 09:30:00)",
			time.Date(2026, 1, 15, 9, 30, 0, 0, time.UTC),
		},
		{
			"datetime(2026-01-15 09:30:00.000)",
			time.Date(2026, 1, 15, 9, 30, 0, 0, time.UTC),
		},
		{
			"datetime(2026-01-15 09:30:00.123)",
			time.Date(2026, 1, 15, 9, 30, 0, 123_000_000, time.UTC),
		},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			toks := lexer.Tokenize(tc.src)
			if toks[0].Type != lexer.TokenDatetime {
				t.Fatalf("want TokenDatetime, got %v", toks[0])
			}
			if !toks[0].TimeVal.Equal(tc.want) {
				t.Errorf("TimeVal = %v, want %v", toks[0].TimeVal, tc.want)
			}
		})
	}
}

func TestDatetimeLiteral_Raw(t *testing.T) {
	// Raw field must preserve the full source text.
	toks := lexer.Tokenize("datetime(2026-05-27)")
	if toks[0].Raw != "datetime(2026-05-27)" {
		t.Errorf("Raw = %q, want %q", toks[0].Raw, "datetime(2026-05-27)")
	}
}

// ── Operators and punctuation ─────────────────────────────────────────────────

func TestOperators(t *testing.T) {
	cases := []struct {
		src  string
		want lexer.TokenType
	}{
		{"|", lexer.TokenPipe},
		{"(", lexer.TokenLParen},
		{")", lexer.TokenRParen},
		{",", lexer.TokenComma},
		{".", lexer.TokenDot},
		{"..", lexer.TokenDotDot},
		{"*", lexer.TokenStar},
		{"=", lexer.TokenEq},
		{"==", lexer.TokenEqEq},
		{"!=", lexer.TokenNotEq},
		{"<", lexer.TokenLt},
		{"<=", lexer.TokenLtEq},
		{">", lexer.TokenGt},
		{">=", lexer.TokenGtEq},
		{"=~", lexer.TokenTildeEq},
		{"!in", lexer.TokenNotIn},
		{"+", lexer.TokenPlus},
		{"-", lexer.TokenMinus},
		{"/", lexer.TokenSlash},
		{"$left", lexer.TokenDollarLeft},
		{"$right", lexer.TokenDollarRight},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			got := types(tc.src)
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("Tokenize(%q) types = %v, want [%v]", tc.src, got, tc.want)
			}
		})
	}
}

func TestNotIn_NotMatchedInsideWord(t *testing.T) {
	// "!info" must not lex as TokenNotIn; "!" alone is an error.
	toks := lexer.Tokenize("!info")
	if toks[0].Type == lexer.TokenNotIn {
		t.Errorf("!info should not produce TokenNotIn")
	}
}

func TestFieldRef(t *testing.T) {
	// "table.column" → IDENT DOT IDENT
	got := types("table.column")
	want := []lexer.TokenType{lexer.TokenIdent, lexer.TokenDot, lexer.TokenIdent}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("got %v, want %v", got, want)
	}
}

// ── Line comments ─────────────────────────────────────────────────────────────

func TestLineComment(t *testing.T) {
	src := "Logs // everything after this is ignored\n| where"
	got := types(src)
	want := []lexer.TokenType{lexer.TokenIdent, lexer.TokenPipe, lexer.TokenWhere}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("got %v, want %v", got, want)
	}
}

// ── Line and column tracking ──────────────────────────────────────────────────

func TestLineTracking(t *testing.T) {
	toks := lexer.Tokenize("Logs\n| where")
	// "Logs" is on line 1
	if toks[0].Line != 1 {
		t.Errorf("Logs: want line 1, got %d", toks[0].Line)
	}
	// "|" is on line 2
	if toks[1].Line != 2 {
		t.Errorf("|: want line 2, got %d", toks[1].Line)
	}
}

// ── Full query ────────────────────────────────────────────────────────────────

func TestFullQuery(t *testing.T) {
	src := `Logs(last 24h)
| where statusCode >= 400
| project timestamp, region
| take 3`

	want := []lexer.TokenType{
		lexer.TokenIdent,   // Logs
		lexer.TokenLParen,  // (
		lexer.TokenLast,    // last
		lexer.TokenInt,     // 24
		lexer.TokenIdent,   // h   (timespan unit; parser matches it)
		lexer.TokenRParen,  // )
		lexer.TokenPipe,    // |
		lexer.TokenWhere,   // where
		lexer.TokenIdent,   // statusCode
		lexer.TokenGtEq,    // >=
		lexer.TokenInt,     // 400
		lexer.TokenPipe,    // |
		lexer.TokenProject, // project
		lexer.TokenIdent,   // timestamp
		lexer.TokenComma,   // ,
		lexer.TokenIdent,   // region
		lexer.TokenPipe,    // |
		lexer.TokenTake,    // take
		lexer.TokenInt,     // 3
	}

	got := types(src)
	if len(got) != len(want) {
		t.Fatalf("got %d tokens, want %d\n  got: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token[%d]: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestAbsoluteRangeQuery(t *testing.T) {
	src := `Logs(datetime(2026-01-15 09:00:00) .. datetime(2026-01-15 18:00:00))`
	want := []lexer.TokenType{
		lexer.TokenIdent,    // Logs
		lexer.TokenLParen,   // (
		lexer.TokenDatetime, // datetime(...)
		lexer.TokenDotDot,   // ..
		lexer.TokenDatetime, // datetime(...)
		lexer.TokenRParen,   // )
	}
	got := types(src)
	if len(got) != len(want) {
		t.Fatalf("got %d tokens, want %d\n  got: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token[%d]: got %v, want %v", i, got[i], want[i])
		}
	}
}

// ── Error cases ───────────────────────────────────────────────────────────────

func TestError_UnterminatedString(t *testing.T) {
	toks := lexer.Tokenize(`"not closed`)
	if toks[0].Type != lexer.TokenError {
		t.Errorf("want TokenError, got %v", toks[0])
	}
}

func TestError_InvalidDatetime(t *testing.T) {
	toks := lexer.Tokenize(`datetime(not-a-date)`)
	if toks[0].Type != lexer.TokenError {
		t.Errorf("want TokenError, got %v", toks[0])
	}
}

func TestError_DatetimeMissingParen(t *testing.T) {
	toks := lexer.Tokenize(`datetime`)
	if toks[0].Type != lexer.TokenError {
		t.Errorf("want TokenError, got %v", toks[0])
	}
}

func TestError_UnknownCharacter(t *testing.T) {
	toks := lexer.Tokenize(`@foo`)
	if toks[0].Type != lexer.TokenError {
		t.Errorf("want TokenError, got %v", toks[0])
	}
}

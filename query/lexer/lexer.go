package lexer

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Lexer tokenises a single Gobbler query string.
// Call Next() repeatedly; TokenEOF signals end of input, TokenError a lexical fault.
type Lexer struct {
	src  string
	pos  int // byte offset of the next character to consume
	line int // current line (1-based)
	col  int // current column (1-based, byte-level)
}

// New returns a Lexer ready to tokenise src.
func New(src string) *Lexer {
	return &Lexer{src: src, line: 1, col: 1}
}

// Tokenize is a convenience wrapper that returns all tokens, including the
// terminal EOF. It stops early and includes the token on TokenError.
func Tokenize(src string) []Token {
	l := New(src)
	var out []Token
	for {
		tok := l.Next()
		out = append(out, tok)
		if tok.Type == TokenEOF || tok.Type == TokenError {
			break
		}
	}
	return out
}

// Next returns the next token. Once TokenEOF or TokenError has been returned,
// subsequent calls return the same terminal token.
func (l *Lexer) Next() Token {
	l.skipWhitespaceAndComments()

	if l.pos >= len(l.src) {
		return l.tok(TokenEOF, "", l.line, l.col)
	}

	line, col := l.line, l.col
	ch, size := utf8.DecodeRuneInString(l.src[l.pos:])

	switch {
	case isIdentStart(ch):
		return l.lexIdentOrKeyword(line, col)
	case unicode.IsDigit(ch):
		return l.lexNumber(line, col)
	case ch == '"':
		return l.lexString(line, col)
	case ch == '$':
		return l.lexDollar(line, col)
	case ch == '|':
		l.advance(size)
		return l.tok(TokenPipe, "|", line, col)
	case ch == '(':
		l.advance(size)
		return l.tok(TokenLParen, "(", line, col)
	case ch == ')':
		l.advance(size)
		return l.tok(TokenRParen, ")", line, col)
	case ch == ',':
		l.advance(size)
		return l.tok(TokenComma, ",", line, col)
	case ch == '+':
		l.advance(size)
		return l.tok(TokenPlus, "+", line, col)
	case ch == '-':
		l.advance(size)
		return l.tok(TokenMinus, "-", line, col)
	case ch == '/':
		l.advance(size)
		return l.tok(TokenSlash, "/", line, col)
	case ch == '*':
		l.advance(size)
		return l.tok(TokenStar, "*", line, col)
	case ch == '.':
		return l.lexDot(line, col)
	case ch == '=':
		return l.lexEquals(line, col)
	case ch == '!':
		return l.lexBang(line, col)
	case ch == '<':
		return l.lexLt(line, col)
	case ch == '>':
		return l.lexGt(line, col)
	}

	l.advance(size)
	return l.errorf(line, col, "unexpected character %q", ch)
}

// ── Sub-routines ─────────────────────────────────────────────────────────────

func (l *Lexer) lexIdentOrKeyword(line, col int) Token {
	start := l.pos
	_, size := utf8.DecodeRuneInString(l.src[l.pos:])
	l.advance(size)

	// Consume subsequent identifier characters.
	// Grammar: IdentCont ::= [a-zA-Z0-9_-]
	// Practical rule for hyphens: allow a hyphen only when the immediately
	// following character is a letter.  This makes "alpha-folder" one token
	// while keeping "x-1" as three tokens (IDENT, MINUS, INT).
	for l.pos < len(l.src) {
		ch, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_' {
			l.advance(size)
			continue
		}
		if ch == '-' {
			nextPos := l.pos + size
			if nextPos < len(l.src) {
				nextCh, _ := utf8.DecodeRuneInString(l.src[nextPos:])
				if unicode.IsLetter(nextCh) {
					l.advance(size) // consume the hyphen; next iteration grabs the letter
					continue
				}
			}
		}
		break
	}

	word := l.src[start:l.pos]

	// "datetime" must be followed by "(...)" and is consumed as one token.
	if word == "datetime" {
		return l.lexDatetimeLit(line, col, start)
	}

	if tt, ok := keywords[word]; ok {
		return l.tok(tt, word, line, col)
	}

	t := l.tok(TokenIdent, word, line, col)
	t.StrVal = word
	return t
}

// lexDatetimeLit is called after the word "datetime" has been consumed.
// It reads the mandatory "(" DateStr ")" and returns a single TokenDatetime.
func (l *Lexer) lexDatetimeLit(line, col int, rawStart int) Token {
	l.skipWhitespaceAndComments()
	if l.pos >= len(l.src) || l.src[l.pos] != '(' {
		return l.errorf(line, col, "'datetime' must be followed by '('")
	}
	l.advance(1) // '('

	contentStart := l.pos
	for l.pos < len(l.src) && l.src[l.pos] != ')' {
		if l.src[l.pos] == '\n' {
			return l.errorf(line, col, "unterminated datetime literal")
		}
		l.advance(1)
	}
	if l.pos >= len(l.src) {
		return l.errorf(line, col, "unterminated datetime literal")
	}
	content := strings.TrimSpace(l.src[contentStart:l.pos])
	l.advance(1) // ')'

	tv, err := parseDateStr(content)
	if err != nil {
		return l.errorf(line, col, "%v", err)
	}

	t := l.tok(TokenDatetime, l.src[rawStart:l.pos], line, col)
	t.TimeVal = tv
	return t
}

func (l *Lexer) lexNumber(line, col int) Token {
	start := l.pos
	for l.pos < len(l.src) && unicode.IsDigit(rune(l.src[l.pos])) {
		l.advance(1)
	}
	// Float: digit+ "." digit+  — but not ".." (range operator).
	if l.pos < len(l.src) && l.src[l.pos] == '.' &&
		l.pos+1 < len(l.src) && unicode.IsDigit(rune(l.src[l.pos+1])) {
		l.advance(1) // '.'
		for l.pos < len(l.src) && unicode.IsDigit(rune(l.src[l.pos])) {
			l.advance(1)
		}
		raw := l.src[start:l.pos]
		v, _ := strconv.ParseFloat(raw, 64)
		t := l.tok(TokenFloat, raw, line, col)
		t.FloatVal = v
		return t
	}
	raw := l.src[start:l.pos]
	v, _ := strconv.ParseInt(raw, 10, 64)
	t := l.tok(TokenInt, raw, line, col)
	t.IntVal = v
	return t
}

func (l *Lexer) lexString(line, col int) Token {
	rawStart := l.pos
	l.advance(1) // opening '"'
	var sb strings.Builder
	for {
		if l.pos >= len(l.src) || l.src[l.pos] == '\n' || l.src[l.pos] == '\r' {
			return l.errorf(line, col, "unterminated string literal")
		}
		ch := l.src[l.pos]
		if ch == '"' {
			l.advance(1)
			break
		}
		if ch == '\\' {
			l.advance(1)
			if l.pos >= len(l.src) {
				return l.errorf(line, col, "unterminated escape sequence")
			}
			switch l.src[l.pos] {
			case '"':
				sb.WriteByte('"')
			case '\\':
				sb.WriteByte('\\')
			case 'n':
				sb.WriteByte('\n')
			case 'r':
				sb.WriteByte('\r')
			case 't':
				sb.WriteByte('\t')
			default:
				return l.errorf(line, col, "unknown escape sequence \\%c", l.src[l.pos])
			}
			l.advance(1)
			continue
		}
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		sb.WriteRune(r)
		l.advance(size)
	}
	t := l.tok(TokenString, l.src[rawStart:l.pos], line, col)
	t.StrVal = sb.String()
	return t
}

// lexDollar handles $left and $right.
func (l *Lexer) lexDollar(line, col int) Token {
	l.advance(1) // '$'
	if strings.HasPrefix(l.src[l.pos:], "left") && !isIdentContinue(l.runeAt(l.pos+4)) {
		l.advance(4)
		return l.tok(TokenDollarLeft, "$left", line, col)
	}
	if strings.HasPrefix(l.src[l.pos:], "right") && !isIdentContinue(l.runeAt(l.pos+5)) {
		l.advance(5)
		return l.tok(TokenDollarRight, "$right", line, col)
	}
	return l.errorf(line, col, "expected '$left' or '$right'")
}

func (l *Lexer) lexDot(line, col int) Token {
	l.advance(1) // first '.'
	if l.pos < len(l.src) && l.src[l.pos] == '.' {
		l.advance(1)
		return l.tok(TokenDotDot, "..", line, col)
	}
	return l.tok(TokenDot, ".", line, col)
}

func (l *Lexer) lexEquals(line, col int) Token {
	l.advance(1) // '='
	if l.pos < len(l.src) {
		switch l.src[l.pos] {
		case '=':
			l.advance(1)
			return l.tok(TokenEqEq, "==", line, col)
		case '~':
			l.advance(1)
			return l.tok(TokenTildeEq, "=~", line, col)
		}
	}
	return l.tok(TokenEq, "=", line, col)
}

func (l *Lexer) lexBang(line, col int) Token {
	l.advance(1) // '!'
	if l.pos < len(l.src) {
		switch l.src[l.pos] {
		case '=':
			l.advance(1)
			return l.tok(TokenNotEq, "!=", line, col)
		case 'i':
			// "!in" is only valid when "in" is not followed by an identifier char.
			if strings.HasPrefix(l.src[l.pos:], "in") && !isIdentContinue(l.runeAt(l.pos+2)) {
				l.advance(2)
				return l.tok(TokenNotIn, "!in", line, col)
			}
		}
	}
	return l.errorf(line, col, "expected '!=' or '!in'")
}

func (l *Lexer) lexLt(line, col int) Token {
	l.advance(1)
	if l.pos < len(l.src) && l.src[l.pos] == '=' {
		l.advance(1)
		return l.tok(TokenLtEq, "<=", line, col)
	}
	return l.tok(TokenLt, "<", line, col)
}

func (l *Lexer) lexGt(line, col int) Token {
	l.advance(1)
	if l.pos < len(l.src) && l.src[l.pos] == '=' {
		l.advance(1)
		return l.tok(TokenGtEq, ">=", line, col)
	}
	return l.tok(TokenGt, ">", line, col)
}

// ── Whitespace and comment skipping ──────────────────────────────────────────

func (l *Lexer) skipWhitespaceAndComments() {
	for l.pos < len(l.src) {
		switch l.src[l.pos] {
		case ' ', '\t', '\r', '\n':
			l.advance(1)
		case '/':
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '/' {
				// Line comment: skip to end of line.
				for l.pos < len(l.src) && l.src[l.pos] != '\n' {
					l.advance(1)
				}
			} else {
				return
			}
		default:
			return
		}
	}
}

// ── Low-level helpers ─────────────────────────────────────────────────────────

// advance moves pos forward by n bytes, updating line/col tracking.
// n should be the exact byte width of the rune(s) being consumed.
func (l *Lexer) advance(n int) {
	for i := 0; i < n && l.pos < len(l.src); i++ {
		if l.src[l.pos] == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		l.pos++
	}
}

// runeAt returns the rune at byte offset pos, or 0 if out of range.
func (l *Lexer) runeAt(pos int) rune {
	if pos >= len(l.src) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.src[pos:])
	return r
}

func (l *Lexer) tok(tt TokenType, raw string, line, col int) Token {
	return Token{Type: tt, Raw: raw, Line: line, Col: col}
}

func (l *Lexer) errorf(line, col int, format string, args ...any) Token {
	return Token{Type: TokenError, Raw: fmt.Sprintf(format, args...), Line: line, Col: col}
}

// ── Character classification ──────────────────────────────────────────────────

func isIdentStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_'
}

// isIdentContinue is used for boundary checks (e.g. "does an identifier
// continue past this point?").  Hyphens are included because they are valid
// IdentCont characters; the lexer's hyphen-before-letter rule is applied
// inside lexIdentOrKeyword, not here.
func isIdentContinue(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

// ── Date parsing ──────────────────────────────────────────────────────────────

// parseDateStr parses the content of a datetime(...) literal using Gobbler's
// native datetime format (space separator, no timezone, milliseconds optional).
//
// Accepted forms:
//
//	YYYY-MM-DD
//	YYYY-MM-DD HH:MM
//	YYYY-MM-DD HH:MM:SS
//	YYYY-MM-DD HH:MM:SS.mmm
func parseDateStr(s string) (time.Time, error) {
	for _, layout := range []string{
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf(
		"cannot parse %q as datetime: expected YYYY-MM-DD [HH:MM[:SS[.mmm]]]", s)
}

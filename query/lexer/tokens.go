package lexer

import (
	"fmt"
	"time"
)

// TokenType identifies the kind of a lexical token.
type TokenType int

const (
	// ── Literals ──────────────────────────────────────────────────────────
	TokenIdent    TokenType = iota // bare identifier (not a keyword)
	TokenInt                       // e.g. 42
	TokenFloat                     // e.g. 3.14
	TokenString                    // e.g. "hello world"
	TokenDatetime                  // datetime(2026-01-15 09:30:00.000) — fully parsed

	// ── Keywords ──────────────────────────────────────────────────────────
	TokenWhere      // where
	TokenProject    // project
	TokenSummarize  // summarize
	TokenBy         // by
	TokenJoin       // join
	TokenOn         // on
	TokenSort       // sort
	TokenAsc        // asc
	TokenDesc       // desc
	TokenTake       // take
	TokenCount      // count
	TokenOr         // or
	TokenAnd        // and
	TokenNot        // not
	TokenIn         // in
	TokenBetween    // between
	TokenIsnull     // isnull
	TokenIsnotnull  // isnotnull
	TokenIsempty    // isempty
	TokenTrue       // true
	TokenFalse      // false
	TokenAgo        // ago
	TokenLast       // last
	TokenSum        // sum
	TokenAvg        // avg
	TokenMin        // min
	TokenMax        // max
	TokenDcount     // dcount
	TokenContains   // contains
	TokenStartswith // startswith
	TokenEndswith   // endswith

	// ── Punctuation / operators ────────────────────────────────────────────
	TokenPipe        // |
	TokenLParen      // (
	TokenRParen      // )
	TokenComma       // ,
	TokenDot         // .
	TokenDotDot      // ..
	TokenStar        // *
	TokenEq          // =   (alias assignment in project)
	TokenEqEq        // ==
	TokenNotEq       // !=
	TokenLt          // <
	TokenLtEq        // <=
	TokenGt          // >
	TokenGtEq        // >=
	TokenTildeEq     // =~  (case-insensitive equality)
	TokenNotIn       // !in
	TokenPlus        // +
	TokenMinus       // -
	TokenSlash       // /
	TokenDollarLeft  // $left
	TokenDollarRight // $right

	// ── Control ───────────────────────────────────────────────────────────
	TokenEOF   // end of input
	TokenError // lexer error; Raw holds the error message
)

// keywords maps reserved words to their token type.
// "datetime" is absent: the lexer handles it specially because the full
// datetime(...) form must be consumed as a single token.
var keywords = map[string]TokenType{
	"where":      TokenWhere,
	"project":    TokenProject,
	"summarize":  TokenSummarize,
	"by":         TokenBy,
	"join":       TokenJoin,
	"on":         TokenOn,
	"sort":       TokenSort,
	"asc":        TokenAsc,
	"desc":       TokenDesc,
	"take":       TokenTake,
	"count":      TokenCount,
	"or":         TokenOr,
	"and":        TokenAnd,
	"not":        TokenNot,
	"in":         TokenIn,
	"between":    TokenBetween,
	"isnull":     TokenIsnull,
	"isnotnull":  TokenIsnotnull,
	"isempty":    TokenIsempty,
	"true":       TokenTrue,
	"false":      TokenFalse,
	"ago":        TokenAgo,
	"last":       TokenLast,
	"sum":        TokenSum,
	"avg":        TokenAvg,
	"min":        TokenMin,
	"max":        TokenMax,
	"dcount":     TokenDcount,
	"contains":   TokenContains,
	"startswith": TokenStartswith,
	"endswith":   TokenEndswith,
}

// Token is a single lexical unit produced by the Lexer.
type Token struct {
	Type     TokenType
	Raw      string    // exact source text; error message for TokenError
	StrVal   string    // unescaped content for TokenString; text for TokenIdent
	IntVal   int64     // parsed value for TokenInt
	FloatVal float64   // parsed value for TokenFloat
	TimeVal  time.Time // parsed value for TokenDatetime
	Line     int       // 1-based line number of the first character
	Col      int       // 1-based column of the first character (byte-level)
}

// String returns a compact human-readable description, useful for test output.
func (t Token) String() string {
	switch t.Type {
	case TokenIdent:
		return fmt.Sprintf("IDENT(%s)@%d:%d", t.StrVal, t.Line, t.Col)
	case TokenInt:
		return fmt.Sprintf("INT(%d)@%d:%d", t.IntVal, t.Line, t.Col)
	case TokenFloat:
		return fmt.Sprintf("FLOAT(%g)@%d:%d", t.FloatVal, t.Line, t.Col)
	case TokenString:
		return fmt.Sprintf("STRING(%q)@%d:%d", t.StrVal, t.Line, t.Col)
	case TokenDatetime:
		return fmt.Sprintf("DATETIME(%s)@%d:%d", t.TimeVal.Format("2006-01-02 15:04:05.000"), t.Line, t.Col)
	case TokenEOF:
		return fmt.Sprintf("EOF@%d:%d", t.Line, t.Col)
	case TokenError:
		return fmt.Sprintf("ERROR(%s)@%d:%d", t.Raw, t.Line, t.Col)
	default:
		return fmt.Sprintf("%q@%d:%d", t.Raw, t.Line, t.Col)
	}
}

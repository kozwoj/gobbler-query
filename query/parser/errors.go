package parser

import (
	"fmt"

	"github.com/kozwoj/gobbler-query/query/lexer"
)

// ParseError is a syntax error with source-position information.
type ParseError struct {
	Line int
	Col  int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Msg)
}

// describeType returns a short, human-readable name for a token type,
// suitable for use in error messages.
func describeType(tt lexer.TokenType) string {
	switch tt {
	case lexer.TokenIdent:
		return "identifier"
	case lexer.TokenInt:
		return "integer literal"
	case lexer.TokenFloat:
		return "float literal"
	case lexer.TokenString:
		return "string literal"
	case lexer.TokenDatetime:
		return "datetime(...)"
	case lexer.TokenWhere:
		return "'where'"
	case lexer.TokenProject:
		return "'project'"
	case lexer.TokenSummarize:
		return "'summarize'"
	case lexer.TokenBy:
		return "'by'"
	case lexer.TokenJoin:
		return "'join'"
	case lexer.TokenOn:
		return "'on'"
	case lexer.TokenSort:
		return "'sort'"
	case lexer.TokenAsc:
		return "'asc'"
	case lexer.TokenDesc:
		return "'desc'"
	case lexer.TokenTake:
		return "'take'"
	case lexer.TokenCount:
		return "'count'"
	case lexer.TokenOr:
		return "'or'"
	case lexer.TokenAnd:
		return "'and'"
	case lexer.TokenNot:
		return "'not'"
	case lexer.TokenIn:
		return "'in'"
	case lexer.TokenBetween:
		return "'between'"
	case lexer.TokenIsnull:
		return "'isnull'"
	case lexer.TokenIsnotnull:
		return "'isnotnull'"
	case lexer.TokenIsempty:
		return "'isempty'"
	case lexer.TokenTrue:
		return "'true'"
	case lexer.TokenFalse:
		return "'false'"
	case lexer.TokenAgo:
		return "'ago'"
	case lexer.TokenLast:
		return "'last'"
	case lexer.TokenSum:
		return "'sum'"
	case lexer.TokenAvg:
		return "'avg'"
	case lexer.TokenMin:
		return "'min'"
	case lexer.TokenMax:
		return "'max'"
	case lexer.TokenDcount:
		return "'dcount'"
	case lexer.TokenContains:
		return "'contains'"
	case lexer.TokenStartswith:
		return "'startswith'"
	case lexer.TokenEndswith:
		return "'endswith'"
	case lexer.TokenPipe:
		return "'|'"
	case lexer.TokenLParen:
		return "'('"
	case lexer.TokenRParen:
		return "')'"
	case lexer.TokenComma:
		return "','"
	case lexer.TokenDot:
		return "'.'"
	case lexer.TokenDotDot:
		return "'..'"
	case lexer.TokenStar:
		return "'*'"
	case lexer.TokenEq:
		return "'='"
	case lexer.TokenEqEq:
		return "'=='"
	case lexer.TokenNotEq:
		return "'!='"
	case lexer.TokenLt:
		return "'<'"
	case lexer.TokenLtEq:
		return "'<='"
	case lexer.TokenGt:
		return "'>'"
	case lexer.TokenGtEq:
		return "'>='"
	case lexer.TokenTildeEq:
		return "'=~'"
	case lexer.TokenNotIn:
		return "'!in'"
	case lexer.TokenPlus:
		return "'+'"
	case lexer.TokenMinus:
		return "'-'"
	case lexer.TokenSlash:
		return "'/'"
	case lexer.TokenDollarLeft:
		return "'$left'"
	case lexer.TokenDollarRight:
		return "'$right'"
	case lexer.TokenEOF:
		return "end of input"
	default:
		return fmt.Sprintf("token(%d)", int(tt))
	}
}

// describeToken returns a human-readable description of a token, including
// its raw value when that adds useful diagnostic information.
func describeToken(t lexer.Token) string {
	switch t.Type {
	case lexer.TokenIdent:
		return fmt.Sprintf("identifier %q", t.StrVal)
	case lexer.TokenInt:
		return fmt.Sprintf("integer %d", t.IntVal)
	case lexer.TokenFloat:
		return fmt.Sprintf("float %g", t.FloatVal)
	case lexer.TokenString:
		return fmt.Sprintf("string %q", t.StrVal)
	default:
		return describeType(t.Type)
	}
}

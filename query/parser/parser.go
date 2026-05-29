// Package parser implements a recursive-descent parser with a Pratt
// sub-parser for expressions.  The public entry point is [Parse].
package parser

import (
	"fmt"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/lexer"
)

// Parse tokenises src and returns the root AST node, or the first error
// encountered (a lex error or a parse error).
func Parse(src string) (*ast.Query, error) {
	tokens := lexer.Tokenize(src)
	for _, t := range tokens {
		if t.Type == lexer.TokenError {
			return nil, &ParseError{Line: t.Line, Col: t.Col, Msg: t.Raw}
		}
	}
	p := &parser{tokens: tokens}
	q, err := p.parseQuery()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.TokenEOF); err != nil {
		return nil, err
	}
	return q, nil
}

// parser holds the full token slice and the index of the next token to read.
type parser struct {
	tokens []lexer.Token
	pos    int
}

// peek returns the current token without consuming it.
func (p *parser) peek() lexer.Token {
	return p.tokens[p.pos]
}

// peekType returns the type of the current token.
func (p *parser) peekType() lexer.TokenType {
	return p.tokens[p.pos].Type
}

// advance consumes and returns the current token.
// Repeated calls at EOF return the EOF token without moving past it.
func (p *parser) advance() lexer.Token {
	t := p.tokens[p.pos]
	if t.Type != lexer.TokenEOF {
		p.pos++
	}
	return t
}

// check returns true if the current token has the given type.
func (p *parser) check(tt lexer.TokenType) bool {
	return p.peek().Type == tt
}

// eat advances past the current token if it matches tt, returning the token
// and true.  If it doesn't match it leaves the cursor unchanged and returns
// the zero Token and false.
func (p *parser) eat(tt lexer.TokenType) (lexer.Token, bool) {
	if p.check(tt) {
		return p.advance(), true
	}
	return lexer.Token{}, false
}

// expect advances if the current token matches tt and returns it.
// Otherwise it returns a ParseError.
func (p *parser) expect(tt lexer.TokenType) (lexer.Token, error) {
	if p.check(tt) {
		return p.advance(), nil
	}
	cur := p.peek()
	return lexer.Token{}, p.errorf(cur, "expected %s, got %s", describeType(tt), describeToken(cur))
}

// lookAhead returns the token n positions ahead of the current cursor without
// consuming anything.  lookAhead(0) is equivalent to peek().
func (p *parser) lookAhead(n int) lexer.Token {
	i := p.pos + n
	if i >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1] // EOF
	}
	return p.tokens[i]
}

// errorf creates a ParseError located at tok.
func (p *parser) errorf(tok lexer.Token, format string, args ...any) *ParseError {
	return &ParseError{Line: tok.Line, Col: tok.Col, Msg: fmt.Sprintf(format, args...)}
}

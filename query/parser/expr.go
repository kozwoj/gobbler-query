package parser

import (
	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/lexer"
)

// ─── Binding powers ───────────────────────────────────────────────────────────

// Infix binding powers.  Higher means tighter binding.
// Prefix operators use the same constants as their right-binding power.
const (
	bpNone  = 0  // sentinel / non-operator
	bpOr    = 10 // or
	bpAnd   = 20 // and
	bpNot   = 30 // prefix: not  (right-binding power)
	bpCmp   = 40 // ==  !=  <  <=  >  >=  =~  contains  startswith  endswith  in  !in  between
	bpAdd   = 50 // +  -  (binary)
	bpMul   = 60 // *  /
	bpUnary = 70 // prefix: unary minus  (right-binding power)
)

// infixBP returns the left binding power of tt when used as an infix operator.
// Returns bpNone (0) for tokens that cannot be infix operators.
func infixBP(tt lexer.TokenType) int {
	switch tt {
	case lexer.TokenOr:
		return bpOr
	case lexer.TokenAnd:
		return bpAnd
	case lexer.TokenEqEq, lexer.TokenNotEq,
		lexer.TokenLt, lexer.TokenLtEq,
		lexer.TokenGt, lexer.TokenGtEq,
		lexer.TokenTildeEq,
		lexer.TokenContains, lexer.TokenStartswith, lexer.TokenEndswith,
		lexer.TokenIn, lexer.TokenNotIn,
		lexer.TokenBetween:
		return bpCmp
	case lexer.TokenPlus, lexer.TokenMinus:
		return bpAdd
	case lexer.TokenStar, lexer.TokenSlash:
		return bpMul
	default:
		return bpNone
	}
}

// ─── Entry points ─────────────────────────────────────────────────────────────

// parseBoolExpr parses a full boolean expression (starting at minimum binding
// power bpNone so that all operators, including 'or', are eligible).
func (p *parser) parseBoolExpr() (ast.BoolExpr, error) {
	startTok := p.peek()
	node, err := p.parseExpr(bpNone)
	if err != nil {
		return nil, err
	}
	be, ok := node.(ast.BoolExpr)
	if !ok {
		return nil, p.errorf(startTok, "expected a boolean expression (e.g. a comparison or logical expression)")
	}
	return be, nil
}

// parseScalarExpr parses a scalar arithmetic expression.  The minimum binding
// power is bpCmp, which prevents comparison and logical operators from being
// consumed: the result is guaranteed to satisfy ast.ScalarExpr.
func (p *parser) parseScalarExpr() (ast.ScalarExpr, error) {
	startTok := p.peek()
	node, err := p.parseExpr(bpCmp)
	if err != nil {
		return nil, err
	}
	se, ok := node.(ast.ScalarExpr)
	if !ok {
		return nil, p.errorf(startTok, "expected a scalar expression")
	}
	return se, nil
}

// ─── Pratt driver ─────────────────────────────────────────────────────────────

// parseExpr is the main Pratt loop.  It returns an ast.BoolExpr, ast.ScalarExpr,
// or ast.Literal value; callers type-assert as needed.
func (p *parser) parseExpr(minBP int) (any, error) {
	left, err := p.nud()
	if err != nil {
		return nil, err
	}
	for {
		op := p.peek()
		bp := infixBP(op.Type)
		if bp == bpNone || bp <= minBP {
			break
		}
		left, err = p.led(left, op)
		if err != nil {
			return nil, err
		}
	}
	return left, nil
}

// ─── Null denotation (prefix / primary) ──────────────────────────────────────

// nud handles a token that appears in prefix / primary position.
// It advances the cursor past the token it handles.
func (p *parser) nud() (any, error) {
	tok := p.advance()
	switch tok.Type {

	// ── Literals ────────────────────────────────────────────────────────────

	case lexer.TokenInt:
		return &ast.IntLit{Value: tok.IntVal}, nil

	case lexer.TokenFloat:
		return &ast.FloatLit{Value: tok.FloatVal}, nil

	case lexer.TokenString:
		return &ast.StringLit{Value: tok.StrVal}, nil

	case lexer.TokenTrue:
		return &ast.BoolLit{Value: true}, nil

	case lexer.TokenFalse:
		return &ast.BoolLit{Value: false}, nil

	case lexer.TokenDatetime:
		return &ast.DatetimeLit{Value: tok.TimeVal}, nil

	// ── ago(N unit) ─────────────────────────────────────────────────────────

	case lexer.TokenAgo:
		if _, err := p.expect(lexer.TokenLParen); err != nil {
			return nil, err
		}
		span, err := p.parseTimespanLit()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.TokenRParen); err != nil {
			return nil, err
		}
		return &ast.AgoExpr{Duration: span}, nil

	// ── Field reference (optionally qualified: table.column) ─────────────────

	case lexer.TokenIdent:
		if _, ok := p.eat(lexer.TokenDot); ok {
			col, err := p.expect(lexer.TokenIdent)
			if err != nil {
				return nil, err
			}
			return &ast.FieldRefExpr{Ref: ast.FieldRef{Table: tok.StrVal, Name: col.StrVal}}, nil
		}
		return &ast.FieldRefExpr{Ref: ast.FieldRef{Name: tok.StrVal}}, nil

	// ── Unary minus ──────────────────────────────────────────────────────────

	case lexer.TokenMinus:
		inner, err := p.parseExpr(bpUnary)
		if err != nil {
			return nil, err
		}
		se, ok := inner.(ast.ScalarExpr)
		if !ok {
			return nil, p.errorf(tok, "unary '-' requires a scalar operand")
		}
		return &ast.UnaryMinusExpr{Expr: se}, nil

	// ── Logical not ─────────────────────────────────────────────────────────

	case lexer.TokenNot:
		inner, err := p.parseExpr(bpNot)
		if err != nil {
			return nil, err
		}
		be, ok := inner.(ast.BoolExpr)
		if !ok {
			return nil, p.errorf(tok, "'not' requires a boolean operand")
		}
		return &ast.NotExpr{Expr: be}, nil

	// ── Grouped sub-expression: ( BoolExpr ) or ( ScalarExpr ) ───────────────

	case lexer.TokenLParen:
		inner, err := p.parseExpr(bpNone)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.TokenRParen); err != nil {
			return nil, err
		}
		return inner, nil

	// ── isnull / isnotnull / isempty ( FieldRef ) ────────────────────────────

	case lexer.TokenIsnull, lexer.TokenIsnotnull, lexer.TokenIsempty:
		var kind ast.IsNullKind
		switch tok.Type {
		case lexer.TokenIsnull:
			kind = ast.KindIsNull
		case lexer.TokenIsnotnull:
			kind = ast.KindIsNotNull
		default:
			kind = ast.KindIsEmpty
		}
		if _, err := p.expect(lexer.TokenLParen); err != nil {
			return nil, err
		}
		ref, err := p.parseFieldRef()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.TokenRParen); err != nil {
			return nil, err
		}
		return &ast.IsNullExpr{Kind: kind, Field: ref}, nil

	default:
		return nil, p.errorf(tok, "unexpected token %s in expression", describeToken(tok))
	}
}

// ─── Left denotation (infix) ──────────────────────────────────────────────────

// led handles a token that appears in infix position after a left-hand
// sub-expression.  op is the infix operator token (already peeked but not yet
// consumed); led advances past it before parsing the right operand.
func (p *parser) led(left any, op lexer.Token) (any, error) {
	p.advance() // consume the infix operator

	switch op.Type {

	// ── Logical operators ────────────────────────────────────────────────────

	case lexer.TokenOr:
		leftBE, err := requireBoolExpr(left, op)
		if err != nil {
			return nil, err
		}
		rightNode, err := p.parseExpr(bpOr) // left-associative
		if err != nil {
			return nil, err
		}
		rightBE, err := requireBoolExpr(rightNode, p.peek())
		if err != nil {
			return nil, err
		}
		return &ast.OrExpr{Left: leftBE, Right: rightBE}, nil

	case lexer.TokenAnd:
		leftBE, err := requireBoolExpr(left, op)
		if err != nil {
			return nil, err
		}
		rightNode, err := p.parseExpr(bpAnd) // left-associative
		if err != nil {
			return nil, err
		}
		rightBE, err := requireBoolExpr(rightNode, p.peek())
		if err != nil {
			return nil, err
		}
		return &ast.AndExpr{Left: leftBE, Right: rightBE}, nil

	// ── Comparison operators ─────────────────────────────────────────────────

	case lexer.TokenEqEq, lexer.TokenNotEq,
		lexer.TokenLt, lexer.TokenLtEq,
		lexer.TokenGt, lexer.TokenGtEq,
		lexer.TokenTildeEq,
		lexer.TokenContains, lexer.TokenStartswith, lexer.TokenEndswith:
		leftSE, err := requireScalarExpr(left, op)
		if err != nil {
			return nil, err
		}
		rightNode, err := p.parseExpr(bpCmp) // parse scalar right operand
		if err != nil {
			return nil, err
		}
		rightSE, err := requireScalarExpr(rightNode, p.peek())
		if err != nil {
			return nil, err
		}
		cmpOp, err := tokenToCmpOp(op)
		if err != nil {
			return nil, err
		}
		return &ast.CompareExpr{Left: leftSE, Op: cmpOp, Right: rightSE}, nil

	// ── in / !in ─────────────────────────────────────────────────────────────

	case lexer.TokenIn, lexer.TokenNotIn:
		ref, err := requireFieldRefExpr(left, op)
		if err != nil {
			return nil, err
		}
		vals, err := p.parseLiteralList()
		if err != nil {
			return nil, err
		}
		return &ast.InExpr{Field: ref, Negated: op.Type == lexer.TokenNotIn, Values: vals}, nil

	// ── between ──────────────────────────────────────────────────────────────

	case lexer.TokenBetween:
		ref, err := requireFieldRefExpr(left, op)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.TokenLParen); err != nil {
			return nil, err
		}
		lo, err := p.parseLiteral()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.TokenDotDot); err != nil {
			return nil, err
		}
		hi, err := p.parseLiteral()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.TokenRParen); err != nil {
			return nil, err
		}
		return &ast.BetweenExpr{Field: ref, Lo: lo, Hi: hi}, nil

	// ── Arithmetic ───────────────────────────────────────────────────────────

	case lexer.TokenPlus, lexer.TokenMinus, lexer.TokenStar, lexer.TokenSlash:
		leftSE, err := requireScalarExpr(left, op)
		if err != nil {
			return nil, err
		}
		rightNode, err := p.parseExpr(infixBP(op.Type)) // left-associative
		if err != nil {
			return nil, err
		}
		rightSE, err := requireScalarExpr(rightNode, p.peek())
		if err != nil {
			return nil, err
		}
		binOp, err := tokenToBinOp(op)
		if err != nil {
			return nil, err
		}
		return &ast.BinaryExpr{Left: leftSE, Op: binOp, Right: rightSE}, nil

	default:
		return nil, p.errorf(op, "unexpected infix operator %s", describeToken(op))
	}
}

// ─── Literal list and single literal ─────────────────────────────────────────

// parseLiteralList parses "(" Literal ( "," Literal )* ")".
func (p *parser) parseLiteralList() ([]ast.Literal, error) {
	if _, err := p.expect(lexer.TokenLParen); err != nil {
		return nil, err
	}
	lit, err := p.parseLiteral()
	if err != nil {
		return nil, err
	}
	lits := []ast.Literal{lit}
	for {
		if _, ok := p.eat(lexer.TokenComma); !ok {
			break
		}
		lit, err := p.parseLiteral()
		if err != nil {
			return nil, err
		}
		lits = append(lits, lit)
	}
	if _, err := p.expect(lexer.TokenRParen); err != nil {
		return nil, err
	}
	return lits, nil
}

// parseLiteral parses a single literal value (int, float, string, bool,
// datetime, or ago(...)).
func (p *parser) parseLiteral() (ast.Literal, error) {
	tok := p.peek()
	switch tok.Type {
	case lexer.TokenInt:
		p.advance()
		return &ast.IntLit{Value: tok.IntVal}, nil
	case lexer.TokenFloat:
		p.advance()
		return &ast.FloatLit{Value: tok.FloatVal}, nil
	case lexer.TokenString:
		p.advance()
		return &ast.StringLit{Value: tok.StrVal}, nil
	case lexer.TokenTrue:
		p.advance()
		return &ast.BoolLit{Value: true}, nil
	case lexer.TokenFalse:
		p.advance()
		return &ast.BoolLit{Value: false}, nil
	case lexer.TokenDatetime:
		p.advance()
		return &ast.DatetimeLit{Value: tok.TimeVal}, nil
	case lexer.TokenAgo:
		p.advance()
		if _, err := p.expect(lexer.TokenLParen); err != nil {
			return nil, err
		}
		span, err := p.parseTimespanLit()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.TokenRParen); err != nil {
			return nil, err
		}
		return &ast.AgoExpr{Duration: span}, nil
	default:
		return nil, p.errorf(tok, "expected a literal value, got %s", describeToken(tok))
	}
}

// ─── Type assertion helpers ───────────────────────────────────────────────────

func requireBoolExpr(node any, tok lexer.Token) (ast.BoolExpr, error) {
	if be, ok := node.(ast.BoolExpr); ok {
		return be, nil
	}
	return nil, &ParseError{Line: tok.Line, Col: tok.Col,
		Msg: "expected a boolean expression on left side of '" + tok.Raw + "'"}
}

func requireScalarExpr(node any, tok lexer.Token) (ast.ScalarExpr, error) {
	if se, ok := node.(ast.ScalarExpr); ok {
		return se, nil
	}
	return nil, &ParseError{Line: tok.Line, Col: tok.Col,
		Msg: "expected a scalar expression on left side of '" + tok.Raw + "'"}
}

// requireFieldRefExpr asserts that node is a *ast.FieldRefExpr and returns
// the underlying FieldRef.  Used for the left side of 'in', '!in', 'between'.
func requireFieldRefExpr(node any, tok lexer.Token) (ast.FieldRef, error) {
	fre, ok := node.(*ast.FieldRefExpr)
	if !ok {
		return ast.FieldRef{}, &ParseError{Line: tok.Line, Col: tok.Col,
			Msg: "left side of '" + tok.Raw + "' must be a bare field reference, not a complex expression"}
	}
	return fre.Ref, nil
}

// ─── Op-code mapping helpers ─────────────────────────────────────────────────

func tokenToCmpOp(tok lexer.Token) (ast.CompareOp, error) {
	switch tok.Type {
	case lexer.TokenEqEq:
		return ast.CmpEq, nil
	case lexer.TokenNotEq:
		return ast.CmpNotEq, nil
	case lexer.TokenLt:
		return ast.CmpLt, nil
	case lexer.TokenLtEq:
		return ast.CmpLtEq, nil
	case lexer.TokenGt:
		return ast.CmpGt, nil
	case lexer.TokenGtEq:
		return ast.CmpGtEq, nil
	case lexer.TokenTildeEq:
		return ast.CmpTildeEq, nil
	case lexer.TokenContains:
		return ast.CmpContains, nil
	case lexer.TokenStartswith:
		return ast.CmpStartswith, nil
	case lexer.TokenEndswith:
		return ast.CmpEndswith, nil
	default:
		return 0, &ParseError{Line: tok.Line, Col: tok.Col,
			Msg: "internal: not a comparison operator: " + tok.Raw}
	}
}

func tokenToBinOp(tok lexer.Token) (ast.BinaryOp, error) {
	switch tok.Type {
	case lexer.TokenPlus:
		return ast.BinAdd, nil
	case lexer.TokenMinus:
		return ast.BinSub, nil
	case lexer.TokenStar:
		return ast.BinMul, nil
	case lexer.TokenSlash:
		return ast.BinDiv, nil
	default:
		return 0, &ParseError{Line: tok.Line, Col: tok.Col,
			Msg: "internal: not an arithmetic operator: " + tok.Raw}
	}
}

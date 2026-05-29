package parser

import (
	"time"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/lexer"
)

// ─── Query / Source / TimeWindow ──────────────────────────────────────────────

func (p *parser) parseQuery() (*ast.Query, error) {
	src, err := p.parseSource()
	if err != nil {
		return nil, err
	}
	q := &ast.Query{Source: src}
	for p.check(lexer.TokenPipe) {
		p.advance() // consume |
		stage, err := p.parseStage()
		if err != nil {
			return nil, err
		}
		q.Stages = append(q.Stages, stage)
	}
	return q, nil
}

func (p *parser) parseSource() (ast.Source, error) {
	name, err := p.expect(lexer.TokenIdent)
	if err != nil {
		return ast.Source{}, err
	}
	if _, err := p.expect(lexer.TokenLParen); err != nil {
		return ast.Source{}, err
	}
	tw, err := p.parseTimeWindow()
	if err != nil {
		return ast.Source{}, err
	}
	if _, err := p.expect(lexer.TokenRParen); err != nil {
		return ast.Source{}, err
	}
	return ast.Source{TypeName: name.StrVal, TimeWindow: tw}, nil
}

func (p *parser) parseTimeWindow() (ast.TimeWindow, error) {
	switch p.peekType() {
	case lexer.TokenStar:
		p.advance()
		return &ast.FullScan{}, nil

	case lexer.TokenLast:
		p.advance()
		span, err := p.parseTimespanLit()
		if err != nil {
			return nil, err
		}
		return &ast.RelativeLookback{Duration: span}, nil

	case lexer.TokenDatetime:
		startTok := p.advance()
		if _, err := p.expect(lexer.TokenDotDot); err != nil {
			return nil, err
		}
		endTok, err := p.expect(lexer.TokenDatetime)
		if err != nil {
			return nil, err
		}
		return &ast.AbsoluteRange{
			Start: ast.DatetimeLit{Value: startTok.TimeVal},
			End:   ast.DatetimeLit{Value: endTok.TimeVal},
		}, nil

	default:
		return nil, p.errorf(p.peek(), "expected a time window: *, 'last <duration>', or 'datetime(..) .. datetime(..)'")
	}
}

// ─── Stage dispatch ───────────────────────────────────────────────────────────

func (p *parser) parseStage() (ast.Stage, error) {
	switch p.peekType() {
	case lexer.TokenWhere:
		return p.parseWhereStage()
	case lexer.TokenProject:
		return p.parseProjectStage()
	case lexer.TokenSummarize:
		return p.parseSummarizeStage()
	case lexer.TokenJoin:
		return p.parseJoinStage()
	case lexer.TokenSort:
		return p.parseSortStage()
	case lexer.TokenTake:
		return p.parseTakeStage()
	case lexer.TokenCount:
		return p.parseCountStage()
	default:
		return nil, p.errorf(p.peek(), "expected a pipeline stage keyword (where, project, summarize, join, sort, take, count), got %s", describeToken(p.peek()))
	}
}

// ─── Where ────────────────────────────────────────────────────────────────────

func (p *parser) parseWhereStage() (*ast.WhereStage, error) {
	p.advance() // consume 'where'
	expr, err := p.parseBoolExpr()
	if err != nil {
		return nil, err
	}
	return &ast.WhereStage{Expr: expr}, nil
}

// ─── Project ──────────────────────────────────────────────────────────────────

func (p *parser) parseProjectStage() (*ast.ProjectStage, error) {
	p.advance() // consume 'project'
	item, err := p.parseProjectItem()
	if err != nil {
		return nil, err
	}
	items := []ast.ProjectItem{item}
	for {
		if _, ok := p.eat(lexer.TokenComma); !ok {
			break
		}
		item, err := p.parseProjectItem()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return &ast.ProjectStage{Items: items}, nil
}

// parseProjectItem parses either "ident = ScalarExpr" (aliased) or a bare
// FieldRef.  Requires one token of lookahead to distinguish the two forms.
func (p *parser) parseProjectItem() (ast.ProjectItem, error) {
	if p.check(lexer.TokenIdent) && p.lookAhead(1).Type == lexer.TokenEq {
		alias := p.advance() // identifier
		p.advance()          // consume =
		expr, err := p.parseScalarExpr()
		if err != nil {
			return ast.ProjectItem{}, err
		}
		return ast.ProjectItem{Alias: alias.StrVal, Expr: expr}, nil
	}
	ref, err := p.parseFieldRef()
	if err != nil {
		return ast.ProjectItem{}, err
	}
	return ast.ProjectItem{Expr: &ast.FieldRefExpr{Ref: ref}}, nil
}

// ─── Summarize ────────────────────────────────────────────────────────────────

func (p *parser) parseSummarizeStage() (*ast.SummarizeStage, error) {
	p.advance() // consume 'summarize'
	item, err := p.parseAggItem()
	if err != nil {
		return nil, err
	}
	aggs := []ast.AggItem{item}
	for {
		if _, ok := p.eat(lexer.TokenComma); !ok {
			break
		}
		item, err := p.parseAggItem()
		if err != nil {
			return nil, err
		}
		aggs = append(aggs, item)
	}
	var groupBy []ast.FieldRef
	if _, ok := p.eat(lexer.TokenBy); ok {
		refs, err := p.parseFieldList()
		if err != nil {
			return nil, err
		}
		groupBy = refs
	}
	return &ast.SummarizeStage{Aggs: aggs, GroupBy: groupBy}, nil
}

// parseAggItem parses either "ident = AggCall" (aliased) or a bare AggCall.
func (p *parser) parseAggItem() (ast.AggItem, error) {
	var alias string
	if p.check(lexer.TokenIdent) && p.lookAhead(1).Type == lexer.TokenEq {
		tok := p.advance() // identifier (alias)
		p.advance()        // consume =
		alias = tok.StrVal
	}
	call, err := p.parseAggCall()
	if err != nil {
		return ast.AggItem{}, err
	}
	return ast.AggItem{Alias: alias, Call: call}, nil
}

func (p *parser) parseAggCall() (ast.AggCall, error) {
	funcTok := p.peek()
	var fn ast.AggFunc
	switch funcTok.Type {
	case lexer.TokenCount:
		fn = ast.AggCount
	case lexer.TokenSum:
		fn = ast.AggSum
	case lexer.TokenAvg:
		fn = ast.AggAvg
	case lexer.TokenMin:
		fn = ast.AggMin
	case lexer.TokenMax:
		fn = ast.AggMax
	case lexer.TokenDcount:
		fn = ast.AggDcount
	default:
		return ast.AggCall{}, p.errorf(funcTok, "expected an aggregation function (count, sum, avg, min, max, dcount), got %s", describeToken(funcTok))
	}
	p.advance() // consume function name

	if _, err := p.expect(lexer.TokenLParen); err != nil {
		return ast.AggCall{}, err
	}
	var field *ast.FieldRef
	if fn != ast.AggCount {
		ref, err := p.parseFieldRef()
		if err != nil {
			return ast.AggCall{}, err
		}
		field = &ref
	}
	if _, err := p.expect(lexer.TokenRParen); err != nil {
		return ast.AggCall{}, err
	}
	return ast.AggCall{Func: fn, Field: field}, nil
}

// ─── Join ─────────────────────────────────────────────────────────────────────

func (p *parser) parseJoinStage() (*ast.JoinStage, error) {
	p.advance() // consume 'join'
	if _, err := p.expect(lexer.TokenLParen); err != nil {
		return nil, err
	}
	sub, err := p.parseQuery()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.TokenRParen); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.TokenOn); err != nil {
		return nil, err
	}
	key, err := p.parseJoinKey()
	if err != nil {
		return nil, err
	}
	keys := []ast.JoinKey{key}
	for {
		if _, ok := p.eat(lexer.TokenComma); !ok {
			break
		}
		key, err := p.parseJoinKey()
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return &ast.JoinStage{Right: sub, Keys: keys}, nil
}

// parseJoinKey parses either a same-name shorthand ("fieldName") or an
// explicit mapping ("$left.col == $right.col").
func (p *parser) parseJoinKey() (ast.JoinKey, error) {
	if p.check(lexer.TokenDollarLeft) {
		p.advance() // consume $left
		if _, err := p.expect(lexer.TokenDot); err != nil {
			return nil, err
		}
		leftCol, err := p.expect(lexer.TokenIdent)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.TokenEqEq); err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.TokenDollarRight); err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.TokenDot); err != nil {
			return nil, err
		}
		rightCol, err := p.expect(lexer.TokenIdent)
		if err != nil {
			return nil, err
		}
		return &ast.ExplicitKey{Left: leftCol.StrVal, Right: rightCol.StrVal}, nil
	}
	// Same-name shorthand: bare identifier.
	name, err := p.expect(lexer.TokenIdent)
	if err != nil {
		return nil, p.errorf(p.peek(), "expected a join key: identifier or '$left.col == $right.col'")
	}
	return &ast.SameNameKey{Name: name.StrVal}, nil
}

// ─── Sort ─────────────────────────────────────────────────────────────────────

func (p *parser) parseSortStage() (*ast.SortStage, error) {
	p.advance() // consume 'sort'
	if _, err := p.expect(lexer.TokenBy); err != nil {
		return nil, err
	}
	item, err := p.parseSortItem()
	if err != nil {
		return nil, err
	}
	items := []ast.SortItem{item}
	for {
		if _, ok := p.eat(lexer.TokenComma); !ok {
			break
		}
		item, err := p.parseSortItem()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return &ast.SortStage{Items: items}, nil
}

func (p *parser) parseSortItem() (ast.SortItem, error) {
	ref, err := p.parseFieldRef()
	if err != nil {
		return ast.SortItem{}, err
	}
	dir := ast.SortAsc
	if _, ok := p.eat(lexer.TokenDesc); ok {
		dir = ast.SortDesc
	} else {
		p.eat(lexer.TokenAsc) // optional; asc is the default
	}
	return ast.SortItem{Field: ref, Dir: dir}, nil
}

// ─── Take / Count ─────────────────────────────────────────────────────────────

func (p *parser) parseTakeStage() (*ast.TakeStage, error) {
	p.advance() // consume 'take'
	tok, err := p.expect(lexer.TokenInt)
	if err != nil {
		return nil, err
	}
	if tok.IntVal <= 0 {
		return nil, p.errorf(tok, "'take' count must be a positive integer, got %d", tok.IntVal)
	}
	return &ast.TakeStage{Count: tok.IntVal}, nil
}

func (p *parser) parseCountStage() (*ast.CountStage, error) {
	p.advance() // consume 'count'
	return &ast.CountStage{}, nil
}

// ─── Shared primitives ────────────────────────────────────────────────────────

// parseFieldRef parses an optionally-qualified field reference:
//
//	Identifier ( "." Identifier )?
func (p *parser) parseFieldRef() (ast.FieldRef, error) {
	name, err := p.expect(lexer.TokenIdent)
	if err != nil {
		return ast.FieldRef{}, p.errorf(p.peek(), "expected a field name (identifier)")
	}
	if _, ok := p.eat(lexer.TokenDot); ok {
		col, err := p.expect(lexer.TokenIdent)
		if err != nil {
			return ast.FieldRef{}, err
		}
		return ast.FieldRef{Table: name.StrVal, Name: col.StrVal}, nil
	}
	return ast.FieldRef{Name: name.StrVal}, nil
}

// parseFieldList parses a comma-separated list of field references.
func (p *parser) parseFieldList() ([]ast.FieldRef, error) {
	ref, err := p.parseFieldRef()
	if err != nil {
		return nil, err
	}
	refs := []ast.FieldRef{ref}
	for {
		if _, ok := p.eat(lexer.TokenComma); !ok {
			break
		}
		ref, err := p.parseFieldRef()
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// parseTimespanLit parses a Gobbler-style timespan: an integer or float
// magnitude immediately followed (possibly with whitespace) by an identifier
// whose first character is the primary unit (d/h/m/s/w) and whose optional
// tail is a standard Go duration string.
//
// Examples accepted:
//
//	7d   24h   30m   4s   2w
//	2.5h   0.5d
//	1d12h30m   1h10m10s   500ms
func (p *parser) parseTimespanLit() (ast.TimespanLit, error) {
	magTok := p.peek()
	var mag float64
	switch magTok.Type {
	case lexer.TokenInt:
		p.advance()
		mag = float64(magTok.IntVal)
	case lexer.TokenFloat:
		p.advance()
		mag = magTok.FloatVal
	default:
		return ast.TimespanLit{}, p.errorf(magTok, "expected a numeric magnitude for the timespan, got %s", describeToken(magTok))
	}
	unitTok, err := p.expect(lexer.TokenIdent)
	if err != nil {
		return ast.TimespanLit{}, p.errorf(magTok, "expected a timespan unit after the magnitude (e.g. 7d, 24h, 2.5h, 1d12h30m)")
	}
	dur, err := p.resolveTimespanSuffix(mag, unitTok)
	if err != nil {
		return ast.TimespanLit{}, err
	}
	return ast.TimespanLit{Duration: dur}, nil
}

// resolveTimespanSuffix converts a float magnitude and a unit-suffix identifier
// into a time.Duration.  The first character of suffix is the primary unit
// (d/h/m/s/w or the multi-char aliases ms/us/ns); any remaining characters
// must form a valid Go duration string that is added to the primary component.
func (p *parser) resolveTimespanSuffix(mag float64, tok lexer.Token) (time.Duration, error) {
	suffix := tok.StrVal
	if suffix == "" {
		return 0, p.errorf(tok, "empty timespan unit")
	}
	// Multi-character Go units that start with a letter shared with a primary unit.
	switch suffix {
	case "ms":
		return time.Duration(mag * float64(time.Millisecond)), nil
	case "us":
		return time.Duration(mag * float64(time.Microsecond)), nil
	case "ns":
		return time.Duration(mag * float64(time.Nanosecond)), nil
	}
	// General form: first char is primary unit, remainder is an optional Go
	// duration string (e.g. suffix "d12h30m" → primary days + tail "12h30m").
	var primary time.Duration
	switch suffix[0] {
	case 'd':
		primary = time.Duration(mag * 24 * float64(time.Hour))
	case 'h':
		primary = time.Duration(mag * float64(time.Hour))
	case 'm':
		primary = time.Duration(mag * float64(time.Minute))
	case 's':
		primary = time.Duration(mag * float64(time.Second))
	case 'w':
		primary = time.Duration(mag * 7 * 24 * float64(time.Hour))
	default:
		return 0, p.errorf(tok, "unknown timespan unit %q; expected one of d, h, m, s, w (or ms/us/ns)", string(suffix[0]))
	}
	tail := suffix[1:]
	if tail == "" {
		return primary, nil
	}
	rem, err := time.ParseDuration(tail)
	if err != nil {
		return 0, p.errorf(tok, "invalid timespan %q: %v", suffix, err)
	}
	return primary + rem, nil
}

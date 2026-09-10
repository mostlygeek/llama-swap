package spl

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// Parse compiles SPL source into a Program. It checks syntax, compiles regular
// expressions, and validates namespaces (read-only paths, context path shape).
// Semantic checks that need outside knowledge (protected parameters, apply
// references) are done by Program.Validate and NewLibrary.
func Parse(src string) (*Program, error) {
	tokens, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	prog := &Program{}
	seen := map[string]bool{}

	for p.peek().kind != tokEOF {
		c, err := p.parseClause()
		if err != nil {
			return nil, err
		}
		if c.action == actApply && !seen[c.policy] {
			seen[c.policy] = true
			prog.applies = append(prog.applies, c.policy)
		}
		prog.clauses = append(prog.clauses, c)

		switch p.peek().kind {
		case tokNewline:
			p.next()
		case tokEOF:
		default:
			t := p.peek()
			return nil, errorAt(t.line, t.col, "unexpected %s, expected end of line", t.describe())
		}
	}
	return prog, nil
}

type parser struct {
	tokens []token
	pos    int
}

func (p *parser) peek() token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return p.tokens[len(p.tokens)-1]
}

func (p *parser) next() token {
	t := p.peek()
	if t.kind != tokEOF {
		p.pos++
	}
	return t
}

func (p *parser) expectKeyword(kw string) error {
	t := p.peek()
	if !t.isKeyword(kw) {
		return errorAt(t.line, t.col, "unexpected %s, expected '%s'", t.describe(), kw)
	}
	p.next()
	return nil
}

func (p *parser) expect(kind tokenKind, what string) (token, error) {
	t := p.peek()
	if t.kind != kind {
		return t, errorAt(t.line, t.col, "unexpected %s, expected %s", t.describe(), what)
	}
	return p.next(), nil
}

func (p *parser) parseClause() (clause, error) {
	t := p.peek()
	c := clause{line: t.line, col: t.col}
	if t.kind != tokIdent {
		return c, errorAt(t.line, t.col, "unexpected %s, expected an action (default, set, remove, apply, deny)", t.describe())
	}

	switch t.text {
	case kwDefault, kwSet:
		p.next()
		c.action = actDefault
		if t.text == kwSet {
			c.action = actSet
		}
		path, err := p.parsePath(true)
		if err != nil {
			return c, err
		}
		if err := p.expectKeyword(kwTo); err != nil {
			return c, err
		}
		val, err := p.parseValue()
		if err != nil {
			return c, err
		}
		c.path = path
		c.value = &val

	case kwRemove:
		p.next()
		c.action = actRemove
		path, err := p.parsePath(true)
		if err != nil {
			return c, err
		}
		c.path = path

	case kwApply:
		p.next()
		c.action = actApply
		name, err := p.expect(tokIdent, "a policy name")
		if err != nil {
			return c, err
		}
		c.policy = name.text
		c.line, c.col = name.line, name.col

	case kwDeny:
		p.next()
		c.action = actDeny
		c.status = 403
		if p.peek().kind == tokNumber {
			num := p.next()
			status, err := strconv.Atoi(num.text)
			if err != nil || status < 400 || status > 599 {
				return c, errorAt(num.line, num.col, "deny status must be an integer between 400 and 599")
			}
			c.status = status
		}
		msg, err := p.expect(tokString, "a quoted message")
		if err != nil {
			return c, err
		}
		c.message = msg.text

	default:
		return c, errorAt(t.line, t.col, "unexpected %s, expected an action (default, set, remove, apply, deny)", t.describe())
	}

	if p.peek().isKeyword(kwWhen) {
		p.next()
		cond, err := p.parseOr()
		if err != nil {
			return c, err
		}
		c.cond = cond
	}
	return c, nil
}

// parsePath parses IDENT { "." IDENT | "[" NUMBER "]" } and classifies its
// namespace. write restricts the path to body and context.<key>.
func (p *parser) parsePath(write bool) (*path, error) {
	first, err := p.expect(tokIdent, "a field path")
	if err != nil {
		return nil, err
	}
	segs := []segment{{key: first.text}}
	var text strings.Builder
	text.WriteString(first.text)

	for {
		switch p.peek().kind {
		case tokDot:
			p.next()
			id, err := p.expect(tokIdent, "a field name after '.'")
			if err != nil {
				return nil, err
			}
			segs = append(segs, segment{key: id.text})
			text.WriteString("." + id.text)
		case tokLBracket:
			p.next()
			num, err := p.expect(tokNumber, "an array index")
			if err != nil {
				return nil, err
			}
			idx, err := strconv.Atoi(num.text)
			if err != nil {
				return nil, errorAt(num.line, num.col, "array index must be an integer")
			}
			if _, err := p.expect(tokRBracket, "']'"); err != nil {
				return nil, err
			}
			segs = append(segs, segment{index: idx, isIndex: true})
			text.WriteString("[" + num.text + "]")
		default:
			return classifyPath(segs, text.String(), first.line, first.col, write)
		}
	}
}

// classifyPath assigns a namespace. A bare namespace word with no further
// segments refers to a body key of that name.
func classifyPath(segs []segment, text string, line, col int, write bool) (*path, error) {
	pth := &path{ns: nsBody, segs: segs, text: text, line: line, col: col}
	if len(segs) < 2 || segs[0].isIndex {
		return pth, nil
	}
	rest := segs[1:]
	switch segs[0].key {
	case "body":
		pth.segs = rest
	case "context":
		pth.ns = nsContext
		pth.segs = rest
		if len(rest) != 1 || rest[0].isIndex {
			return nil, errorAt(line, col, "context paths must be context.<key>")
		}
	case "auth":
		pth.ns = nsAuth
		pth.segs = rest
		if write {
			return nil, errorAt(line, col, "auth.* is read-only")
		}
		if len(rest) != 1 || rest[0].isIndex || rest[0].key != "key" {
			return nil, errorAt(line, col, "unknown auth field %q, only auth.key is available", text)
		}
	case "request":
		pth.ns = nsRequest
		pth.segs = rest
		if write {
			return nil, errorAt(line, col, "request.* is read-only")
		}
		valid := false
		switch {
		case len(rest) == 1 && !rest[0].isIndex:
			valid = rest[0].key == "model" || rest[0].key == "path" || rest[0].key == "method"
		case len(rest) == 2 && !rest[0].isIndex && !rest[1].isIndex:
			valid = rest[0].key == "header"
		}
		if !valid {
			return nil, errorAt(line, col, "unknown request field %q, available: request.model, request.path, request.method, request.header.<name>", text)
		}
	}
	return pth, nil
}

// parseValue parses a set/default value: scalar, list or object literal.
func (p *parser) parseValue() (literal, error) {
	t := p.peek()
	switch t.kind {
	case tokObject:
		p.next()
		return literal{kind: kindObject, raw: t.text}, nil
	case tokLBracket:
		p.next()
		var raws []string
		for p.peek().kind != tokRBracket {
			if len(raws) > 0 {
				if _, err := p.expect(tokComma, "',' or ']'"); err != nil {
					return literal{}, err
				}
			}
			elem, err := p.parseValue()
			if err != nil {
				return literal{}, err
			}
			raws = append(raws, elem.raw)
		}
		p.next()
		return literal{kind: kindArray, raw: "[" + strings.Join(raws, ",") + "]"}, nil
	default:
		return p.parseScalar()
	}
}

// parseScalar parses STRING | NUMBER | true | false | null.
func (p *parser) parseScalar() (literal, error) {
	t := p.peek()
	switch t.kind {
	case tokString:
		p.next()
		return literal{kind: kindString, raw: jsonString(t.text), str: t.text}, nil
	case tokNumber:
		p.next()
		num, _ := strconv.ParseFloat(t.text, 64)
		return literal{kind: kindNumber, raw: t.text, num: num}, nil
	case tokIdent:
		switch t.text {
		case kwTrue:
			p.next()
			return literal{kind: kindBool, raw: "true", b: true}, nil
		case kwFalse:
			p.next()
			return literal{kind: kindBool, raw: "false", b: false}, nil
		case kwNull:
			p.next()
			return literal{kind: kindNull, raw: "null"}, nil
		}
	case tokObject, tokLBracket:
		return literal{}, errorAt(t.line, t.col, "object and array literals are only allowed in set and default values")
	}
	return literal{}, errorAt(t.line, t.col, "unexpected %s, expected a value (string, number, true, false, null)", t.describe())
}

func (p *parser) parseOr() (condNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().isKeyword(kwOr) {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = condOr{left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (condNode, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.peek().isKeyword(kwAnd) {
		p.next()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = condAnd{left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseNot() (condNode, error) {
	if p.peek().isKeyword(kwNot) {
		p.next()
		inner, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return condNot{inner: inner}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (condNode, error) {
	if p.peek().kind == tokLParen {
		p.next()
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen, "')'"); err != nil {
			return nil, err
		}
		return inner, nil
	}
	return p.parsePredicate()
}

func (p *parser) parsePredicate() (condNode, error) {
	pth, err := p.parsePath(false)
	if err != nil {
		return nil, err
	}

	t := p.peek()
	switch t.kind {
	case tokEq, tokNeq, tokLt, tokLte, tokGt, tokGte:
		p.next()
		val, err := p.parseScalar()
		if err != nil {
			return nil, err
		}
		return condCmp{path: pth, op: t.kind, value: val}, nil
	case tokIdent:
		switch t.text {
		case kwIs:
			p.next()
			w := p.peek()
			switch {
			case w.isKeyword(kwPresent):
				p.next()
				return condPresent{path: pth, present: true}, nil
			case w.isKeyword(kwMissing):
				p.next()
				return condPresent{path: pth, present: false}, nil
			}
			return nil, errorAt(w.line, w.col, "unexpected %s, expected 'present' or 'missing'", w.describe())
		case kwMatches:
			p.next()
			re, err := p.expect(tokRegex, "a /regex/")
			if err != nil {
				return nil, err
			}
			compiled, err := regexp.Compile(re.text)
			if err != nil {
				return nil, errorAt(re.line, re.col, "invalid regex: %v", err)
			}
			return condMatches{path: pth, re: compiled}, nil
		case kwIn:
			p.next()
			if _, err := p.expect(tokLBracket, "'['"); err != nil {
				return nil, err
			}
			var values []literal
			for p.peek().kind != tokRBracket {
				if len(values) > 0 {
					if _, err := p.expect(tokComma, "',' or ']'"); err != nil {
						return nil, err
					}
				}
				val, err := p.parseScalar()
				if err != nil {
					return nil, err
				}
				values = append(values, val)
			}
			p.next()
			return condIn{path: pth, values: values}, nil
		}
	}
	return nil, errorAt(t.line, t.col, "unexpected %s, expected a comparison operator, 'is', 'matches' or 'in'", t.describe())
}

// jsonString encodes s as a JSON string without HTML escaping, so "</s>"
// stays readable in the forwarded body.
func jsonString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}

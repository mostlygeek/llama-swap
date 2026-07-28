package matrix

import (
	"fmt"
	"strings"
	"unicode"
)

type tokenType int

const (
	tokIdent tokenType = iota
	tokAnd
	tokOr
	tokLParen
	tokRParen
	tokRef
	tokEOF
)

type token struct {
	typ tokenType
	val string
}

type nodeKind int

const (
	nodeLeaf nodeKind = iota
	nodeRef
	nodeAnd
	nodeOr
)

type node struct {
	kind     nodeKind
	name     string
	children []*node
	ref      *node
}

func tokenize(input string) ([]token, error) {
	var tokens []token
	runes := []rune(input)

	for i := 0; i < len(runes); {
		ch := runes[i]
		if unicode.IsSpace(ch) {
			i++
			continue
		}

		switch ch {
		case '&':
			tokens = append(tokens, token{typ: tokAnd, val: "&"})
			i++
		case '|':
			tokens = append(tokens, token{typ: tokOr, val: "|"})
			i++
		case '(':
			tokens = append(tokens, token{typ: tokLParen, val: "("})
			i++
		case ')':
			tokens = append(tokens, token{typ: tokRParen, val: ")"})
			i++
		case '+':
			i++
			start := i
			for i < len(runes) && isIdentChar(runes[i]) {
				i++
			}
			if i == start {
				return nil, fmt.Errorf("expected set name after '+' at position %d", start)
			}
			tokens = append(tokens, token{typ: tokRef, val: string(runes[start:i])})
		default:
			if !isIdentChar(ch) {
				return nil, fmt.Errorf("unexpected character %q at position %d", ch, i)
			}
			start := i
			for i < len(runes) && isIdentChar(runes[i]) {
				i++
			}
			tokens = append(tokens, token{typ: tokIdent, val: string(runes[start:i])})
		}
	}

	return append(tokens, token{typ: tokEOF}), nil
}

func isIdentChar(ch rune) bool {
	return unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_' || ch == '-' || ch == '.'
}

type parser struct {
	tokens []token
	pos    int
}

func parseDSL(input string) (*node, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, fmt.Errorf("empty DSL expression")
	}

	tokens, err := tokenize(input)
	if err != nil {
		return nil, fmt.Errorf("tokenize: %w", err)
	}

	p := &parser{tokens: tokens}
	root, err := p.parseOrExpr()
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if p.peek().typ != tokEOF {
		return nil, fmt.Errorf("parse: unexpected token %q after expression", p.peek().val)
	}
	return root, nil
}

func (p *parser) peek() token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return token{typ: tokEOF}
}

func (p *parser) next() token {
	t := p.peek()
	if t.typ != tokEOF {
		p.pos++
	}
	return t
}

func (p *parser) parseOrExpr() (*node, error) {
	left, err := p.parseAndExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().typ != tokOr {
		return left, nil
	}

	children := []*node{left}
	for p.peek().typ == tokOr {
		p.next()
		right, err := p.parseAndExpr()
		if err != nil {
			return nil, err
		}
		children = append(children, right)
	}
	return &node{kind: nodeOr, children: children}, nil
}

func (p *parser) parseAndExpr() (*node, error) {
	left, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	if p.peek().typ != tokAnd {
		return left, nil
	}

	children := []*node{left}
	for p.peek().typ == tokAnd {
		p.next()
		right, err := p.parseAtom()
		if err != nil {
			return nil, err
		}
		children = append(children, right)
	}
	return &node{kind: nodeAnd, children: children}, nil
}

func (p *parser) parseAtom() (*node, error) {
	t := p.next()
	switch t.typ {
	case tokIdent:
		return &node{kind: nodeLeaf, name: t.val}, nil
	case tokRef:
		return &node{kind: nodeRef, name: t.val}, nil
	case tokLParen:
		root, err := p.parseOrExpr()
		if err != nil {
			return nil, err
		}
		if p.next().typ != tokRParen {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		return root, nil
	default:
		return nil, fmt.Errorf("unexpected token %q", t.val)
	}
}

func walk(root *node, visit func(*node) error) error {
	if err := visit(root); err != nil {
		return err
	}
	for _, child := range root.children {
		if err := walk(child, visit); err != nil {
			return err
		}
	}
	return nil
}

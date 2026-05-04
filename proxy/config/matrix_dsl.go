package config

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

const maxDSLExpansions = 1000

// Token types for the DSL lexer
type tokenType int

const (
	tokIdent  tokenType = iota // model alias or name
	tokAnd                     // &
	tokOr                      // |
	tokLParen                  // (
	tokRParen                  // )
	tokRef                     // +setName
	tokEOF
)

type token struct {
	typ tokenType
	val string
}

// tokenize splits a DSL string into tokens.
func tokenize(input string) ([]token, error) {
	var tokens []token
	i := 0
	runes := []rune(input)

	for i < len(runes) {
		ch := runes[i]

		// skip whitespace
		if unicode.IsSpace(ch) {
			i++
			continue
		}

		switch ch {
		case '&':
			tokens = append(tokens, token{tokAnd, "&"})
			i++
		case '|':
			tokens = append(tokens, token{tokOr, "|"})
			i++
		case '(':
			tokens = append(tokens, token{tokLParen, "("})
			i++
		case ')':
			tokens = append(tokens, token{tokRParen, ")"})
			i++
		case '+':
			// +ref: read the identifier that follows
			i++
			start := i
			for i < len(runes) && isIdentChar(runes[i]) {
				i++
			}
			if i == start {
				return nil, fmt.Errorf("expected set name after '+' at position %d", start)
			}
			tokens = append(tokens, token{tokRef, string(runes[start:i])})
		default:
			if isIdentChar(ch) {
				start := i
				for i < len(runes) && isIdentChar(runes[i]) {
					i++
				}
				tokens = append(tokens, token{tokIdent, string(runes[start:i])})
			} else {
				return nil, fmt.Errorf("unexpected character %q at position %d", ch, i)
			}
		}
	}

	tokens = append(tokens, token{tokEOF, ""})
	return tokens, nil
}

func isIdentChar(ch rune) bool {
	return unicode.IsLetter(ch) || unicode.IsDigit(ch) || strings.ContainsRune("_-./:", ch)
}

// AST node types
type dslNode interface {
	dslNode()
}

type andNode struct {
	children []dslNode
}

type orNode struct {
	children []dslNode
}

type leafNode struct {
	name string
}

type refNode struct {
	setName string
}

func (andNode) dslNode()  {}
func (orNode) dslNode()   {}
func (leafNode) dslNode() {}
func (refNode) dslNode()  {}

// parser holds state for recursive-descent parsing.
type parser struct {
	tokens []token
	pos    int
}

func (p *parser) peek() token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return token{tokEOF, ""}
}

func (p *parser) next() token {
	t := p.peek()
	if t.typ != tokEOF {
		p.pos++
	}
	return t
}

func (p *parser) expect(typ tokenType) (token, error) {
	t := p.next()
	if t.typ != typ {
		return t, fmt.Errorf("expected token type %d, got %q", typ, t.val)
	}
	return t, nil
}

// Grammar:
//
//	expr    = andExpr
//	andExpr = orExpr ('&' orExpr)*
//	orExpr  = atom ('|' atom)*
//	atom    = ident | '+' ident | '(' expr ')'
//
// & binds tighter than |, so "a | b & c" means "a | (b & c)"
func parse(tokens []token) (dslNode, error) {
	p := &parser{tokens: tokens}
	node, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().typ != tokEOF {
		return nil, fmt.Errorf("unexpected token %q after expression", p.peek().val)
	}
	return node, nil
}

func (p *parser) parseExpr() (dslNode, error) {
	return p.parseOrExpr()
}

func (p *parser) parseOrExpr() (dslNode, error) {
	left, err := p.parseAndExpr()
	if err != nil {
		return nil, err
	}

	if p.peek().typ == tokOr {
		children := []dslNode{left}
		for p.peek().typ == tokOr {
			p.next() // consume |
			right, err := p.parseAndExpr()
			if err != nil {
				return nil, err
			}
			children = append(children, right)
		}
		return orNode{children: children}, nil
	}

	return left, nil
}

func (p *parser) parseAndExpr() (dslNode, error) {
	left, err := p.parseAtom()
	if err != nil {
		return nil, err
	}

	if p.peek().typ == tokAnd {
		children := []dslNode{left}
		for p.peek().typ == tokAnd {
			p.next() // consume &
			right, err := p.parseAtom()
			if err != nil {
				return nil, err
			}
			children = append(children, right)
		}
		return andNode{children: children}, nil
	}

	return left, nil
}

func (p *parser) parseAtom() (dslNode, error) {
	t := p.peek()

	switch t.typ {
	case tokIdent:
		p.next()
		return leafNode{name: t.val}, nil

	case tokRef:
		p.next()
		return refNode{setName: t.val}, nil

	case tokLParen:
		p.next() // consume (
		node, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		return node, nil

	default:
		return nil, fmt.Errorf("unexpected token %q", t.val)
	}
}

// expand walks the AST and produces all combinations.
// resolvedRefs contains previously expanded sets for +ref resolution.
func expand(node dslNode, resolvedRefs map[string][][]string) ([][]string, error) {
	switch n := node.(type) {
	case leafNode:
		return [][]string{{n.name}}, nil

	case refNode:
		expanded, ok := resolvedRefs[n.setName]
		if !ok {
			return nil, fmt.Errorf("unknown set reference +%s", n.setName)
		}
		// Return a copy
		result := make([][]string, len(expanded))
		for i, combo := range expanded {
			result[i] = make([]string, len(combo))
			copy(result[i], combo)
		}
		return result, nil

	case orNode:
		// Union of all children's expansions
		var result [][]string
		for _, child := range n.children {
			childResult, err := expand(child, resolvedRefs)
			if err != nil {
				return nil, err
			}
			result = append(result, childResult...)
			if len(result) > maxDSLExpansions {
				return nil, fmt.Errorf("DSL expansion exceeded %d combinations", maxDSLExpansions)
			}
		}
		return result, nil

	case andNode:
		// Cartesian product across children
		result := [][]string{{}} // start with one empty combo
		for _, child := range n.children {
			childResult, err := expand(child, resolvedRefs)
			if err != nil {
				return nil, err
			}
			result, err = cartesianProduct(result, childResult, maxDSLExpansions)
			if err != nil {
				return nil, err
			}
		}
		return result, nil

	default:
		return nil, fmt.Errorf("unknown node type %T", node)
	}
}

// cartesianProduct computes the cartesian product of two sets of combinations.
// It returns an error if the product would exceed cap.
func cartesianProduct(left, right [][]string, cap int) ([][]string, error) {
	if int64(len(left))*int64(len(right)) > int64(cap) {
		return nil, fmt.Errorf("DSL expansion exceeded %d combinations", cap)
	}
	result := make([][]string, 0, len(left)*len(right))
	for _, l := range left {
		for _, r := range right {
			combo := make([]string, 0, len(l)+len(r))
			combo = append(combo, l...)
			combo = append(combo, r...)
			result = append(result, combo)
		}
	}
	return result, nil
}

// ParseAndExpandDSL tokenizes, parses, and expands a DSL string.
// resolvedRefs contains previously expanded sets for +ref inlining.
func ParseAndExpandDSL(dsl string, resolvedRefs map[string][][]string) ([][]string, error) {
	dsl = strings.TrimSpace(dsl)
	if dsl == "" {
		return nil, fmt.Errorf("empty DSL expression")
	}

	tokens, err := tokenize(dsl)
	if err != nil {
		return nil, fmt.Errorf("tokenize: %w", err)
	}

	tree, err := parse(tokens)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	result, err := expand(tree, resolvedRefs)
	if err != nil {
		return nil, err
	}

	// Deduplicate models within each combination and sort for consistency
	for i, combo := range result {
		result[i] = dedupAndSort(combo)
	}

	return result, nil
}

// dedupAndSort removes duplicate entries and sorts alphabetically.
func dedupAndSort(items []string) []string {
	seen := make(map[string]bool, len(items))
	var unique []string
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			unique = append(unique, item)
		}
	}
	sort.Strings(unique)
	return unique
}

// extractRefs scans a DSL string for +ref tokens without full parsing.
// Used for building the dependency graph for topological sorting.
func extractRefs(dsl string) ([]string, error) {
	tokens, err := tokenize(dsl)
	if err != nil {
		return nil, err
	}

	var refs []string
	seen := make(map[string]bool)
	for _, t := range tokens {
		if t.typ == tokRef && !seen[t.val] {
			seen[t.val] = true
			refs = append(refs, t.val)
		}
	}
	return refs, nil
}

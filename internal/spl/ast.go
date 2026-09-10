package spl

import "regexp"

type actionKind int

const (
	actDefault actionKind = iota
	actSet
	actRemove
	actApply
	actDeny
)

func (a actionKind) String() string {
	switch a {
	case actDefault:
		return kwDefault
	case actSet:
		return kwSet
	case actRemove:
		return kwRemove
	case actApply:
		return kwApply
	case actDeny:
		return kwDeny
	}
	return "?"
}

// namespace identifies where a path reads from and writes to.
type namespace int

const (
	nsBody    namespace = iota // JSON request body (default, or explicit body. prefix)
	nsContext                  // context.<key>: request metadata string bag
	nsAuth                     // auth.key: read-only
	nsRequest                  // request.model|path|method|header.<name>: read-only
)

type segment struct {
	key     string
	index   int
	isIndex bool
}

// path is a parsed field path. For nsBody, segs are the body path segments.
// For the other namespaces, segs holds the segments after the namespace word.
type path struct {
	ns   namespace
	segs []segment
	text string // original spelling, for error messages
	line int
	col  int
}

type valueKind int

const (
	kindAbsent valueKind = iota
	kindNull
	kindBool
	kindNumber
	kindString
	kindObject
	kindArray
)

func (k valueKind) String() string {
	switch k {
	case kindAbsent:
		return "absent"
	case kindNull:
		return "null"
	case kindBool:
		return "bool"
	case kindNumber:
		return "number"
	case kindString:
		return "string"
	case kindObject:
		return "object"
	case kindArray:
		return "array"
	}
	return "?"
}

// literal is a constant value from the program source. raw is its JSON
// encoding, used verbatim for body writes.
type literal struct {
	kind valueKind
	raw  string
	str  string
	num  float64
	b    bool
}

type clause struct {
	action  actionKind
	path    *path    // default, set, remove
	value   *literal // default, set
	policy  string   // apply
	status  int      // deny
	message string   // deny
	cond    condNode // nil when the clause has no when
	line    int
	col     int
}

type condNode interface {
	isCond()
}

type condAnd struct{ left, right condNode }
type condOr struct{ left, right condNode }
type condNot struct{ inner condNode }

// condCmp is path OP scalar.
type condCmp struct {
	path  *path
	op    tokenKind
	value literal
}

type condMatches struct {
	path *path
	re   *regexp.Regexp
}

type condPresent struct {
	path    *path
	present bool
}

type condIn struct {
	path   *path
	values []literal
}

func (condAnd) isCond()     {}
func (condOr) isCond()      {}
func (condNot) isCond()     {}
func (condCmp) isCond()     {}
func (condMatches) isCond() {}
func (condPresent) isCond() {}
func (condIn) isCond()      {}

// Program is a parsed, immutable SPL program. It is safe for concurrent use.
type Program struct {
	clauses []clause
	applies []string // policy names referenced by apply, deduplicated in order
}

// Applies returns the policy names this program references with apply.
func (p *Program) Applies() []string {
	out := make([]string, len(p.applies))
	copy(out, p.applies)
	return out
}

// Len returns the number of clauses in the program.
func (p *Program) Len() int {
	return len(p.clauses)
}

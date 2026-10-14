package spl

import (
	"fmt"
	"net/http"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// maxApplyDepth bounds nested apply calls. Cycles are rejected when a Library
// is built, so this only guards against hand-built libraries.
const maxApplyDepth = 32

// Request is the state a program reads and rewrites. Body and Context are
// modified in place by Run.
type Request struct {
	Body    []byte
	Context map[string]string // context.<key>; allocated by Run when nil
	APIKey  string            // auth.key; absent when empty
	Model   string            // request.model: the model ID the client asked for
	Path    string            // request.path
	Method  string            // request.method
	Header  http.Header       // request.header.<name>
}

// Denial is the outcome of a deny action.
type Denial struct {
	Status  int
	Message string
}

// Run evaluates the program against req. Body and Context are updated in
// place. A non-nil Denial means a deny clause fired and processing stopped;
// the request should be rejected with its status and message. An error means
// the body could not be rewritten and the request should fail.
func (p *Program) Run(lib *Library, req *Request) (*Denial, error) {
	if req.Context == nil {
		req.Context = make(map[string]string)
	}
	e := &evaluator{lib: lib, req: req}
	return e.run(p)
}

type evaluator struct {
	lib   *Library
	req   *Request
	depth int
}

func (e *evaluator) run(p *Program) (*Denial, error) {
	for i := range p.clauses {
		c := &p.clauses[i]
		if c.cond != nil && !e.eval(c.cond) {
			continue
		}
		switch c.action {
		case actDefault:
			if e.exists(c.path) {
				continue
			}
			if err := e.set(c.path, c.value); err != nil {
				return nil, err
			}
		case actSet:
			if err := e.set(c.path, c.value); err != nil {
				return nil, err
			}
		case actRemove:
			if err := e.remove(c.path); err != nil {
				return nil, err
			}
		case actApply:
			denied, err := e.apply(c.policy)
			if err != nil {
				return nil, err
			}
			if denied != nil {
				return denied, nil
			}
		case actDeny:
			return &Denial{Status: c.status, Message: c.message}, nil
		}
	}
	return nil, nil
}

func (e *evaluator) apply(name string) (*Denial, error) {
	if e.lib == nil {
		return nil, fmt.Errorf("apply %s: no policy library", name)
	}
	prog, ok := e.lib.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("apply %s: unknown policy", name)
	}
	if e.depth >= maxApplyDepth {
		return nil, fmt.Errorf("apply %s: policy nesting deeper than %d", name, maxApplyDepth)
	}
	e.depth++
	defer func() { e.depth-- }()
	return e.run(prog)
}

// read resolves a path to its current value.
func (e *evaluator) read(p *path) value {
	switch p.ns {
	case nsBody:
		gp, ok := p.gjsonPath(e.req.Body)
		if !ok {
			return absent
		}
		return fromResult(gjson.GetBytes(e.req.Body, gp))
	case nsContext:
		if v, ok := e.req.Context[p.key()]; ok {
			return stringValue(v)
		}
		return absent
	case nsAuth:
		if e.req.APIKey == "" {
			return absent
		}
		return stringValue(e.req.APIKey)
	case nsRequest:
		var s string
		switch p.segs[0].key {
		case "model":
			s = e.req.Model
		case "path":
			s = e.req.Path
		case "method":
			s = e.req.Method
		case "header":
			if e.req.Header != nil {
				s = e.req.Header.Get(p.segs[1].key)
			}
		}
		if s == "" {
			return absent
		}
		return stringValue(s)
	}
	return absent
}

func (e *evaluator) exists(p *path) bool {
	return e.read(p).kind != kindAbsent
}

func (e *evaluator) set(p *path, lit *literal) error {
	switch p.ns {
	case nsBody:
		gp, ok := p.gjsonPath(e.req.Body)
		if !ok {
			return nil
		}
		body, err := sjson.SetRawBytes(e.req.Body, gp, []byte(lit.raw))
		if err != nil {
			return fmt.Errorf("set %s: %w", p.text, err)
		}
		e.req.Body = body
	case nsContext:
		s, ok := lit.contextString()
		if !ok {
			delete(e.req.Context, p.key())
			return nil
		}
		e.req.Context[p.key()] = s
	}
	return nil
}

func (e *evaluator) remove(p *path) error {
	switch p.ns {
	case nsBody:
		gp, ok := p.gjsonPath(e.req.Body)
		if !ok || !gjson.GetBytes(e.req.Body, gp).Exists() {
			return nil
		}
		body, err := sjson.DeleteBytes(e.req.Body, gp)
		if err != nil {
			return fmt.Errorf("remove %s: %w", p.text, err)
		}
		e.req.Body = body
	case nsContext:
		delete(e.req.Context, p.key())
	}
	return nil
}

func (e *evaluator) eval(c condNode) bool {
	switch n := c.(type) {
	case condAnd:
		return e.eval(n.left) && e.eval(n.right)
	case condOr:
		return e.eval(n.left) || e.eval(n.right)
	case condNot:
		return !e.eval(n.inner)
	case condCmp:
		v := e.read(n.path)
		switch n.op {
		case tokEq:
			return v.equals(n.value)
		case tokNeq:
			return !v.equals(n.value)
		default:
			return v.compare(n.op, n.value)
		}
	case condMatches:
		v := e.read(n.path)
		return v.kind == kindString && n.re.MatchString(v.str)
	case condPresent:
		return e.exists(n.path) == n.present
	case condIn:
		v := e.read(n.path)
		for _, lit := range n.values {
			if v.equals(lit) {
				return true
			}
		}
		return false
	}
	return false
}

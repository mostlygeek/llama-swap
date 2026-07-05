package router

import (
	"container/list"
	"context"
)

// serializeGate is an optional depth-1 gate for upstream inference handlers.
// It is independent from the scheduler's per-model in-flight accounting: the
// scheduler may grant many handlers, but each granted handler acquires this gate
// immediately before calling the upstream process and releases it when that call
// returns.
type serializeGate struct {
	reqCh     chan serializeAcquireReq
	releaseCh chan struct{}
}

type serializeAcquireReq struct {
	priority int
	seq      int64
	grant    chan struct{}
	done     <-chan struct{}
}

func newSerializeGate() *serializeGate {
	g := &serializeGate{
		reqCh:     make(chan serializeAcquireReq),
		releaseCh: make(chan struct{}),
	}
	go g.run()
	return g
}

func (g *serializeGate) acquire(ctx context.Context, priority int, seq int64) (func(), bool) {
	if g == nil {
		return func() {}, true
	}
	grant := make(chan struct{})
	req := serializeAcquireReq{priority: priority, seq: seq, grant: grant, done: ctx.Done()}
	select {
	case g.reqCh <- req:
	case <-ctx.Done():
		return nil, false
	}
	select {
	case <-grant:
		return func() {
			select {
			case g.releaseCh <- struct{}{}:
			case <-ctx.Done():
				g.releaseCh <- struct{}{}
			}
		}, true
	case <-ctx.Done():
		select {
		case <-grant:
			return func() {
				select {
				case g.releaseCh <- struct{}{}:
				case <-ctx.Done():
					g.releaseCh <- struct{}{}
				}
			}, true
		default:
			return nil, false
		}
	}
}

func (g *serializeGate) run() {
	var busy bool
	waiters := list.New()
	for {
		if !busy {
			for {
				front := bestSerializeWaiter(waiters)
				if front == nil {
					break
				}
				req := waiters.Remove(front).(serializeAcquireReq)
				select {
				case req.grant <- struct{}{}:
					busy = true
				case <-req.done:
					continue
				}
				break
			}
		}

		select {
		case req := <-g.reqCh:
			if busy {
				insertSerializeWaiter(waiters, req)
				continue
			}
			select {
			case req.grant <- struct{}{}:
				busy = true
			case <-req.done:
			}
		case <-g.releaseCh:
			busy = false
		}
	}
}

func insertSerializeWaiter(waiters *list.List, req serializeAcquireReq) {
	for e := waiters.Front(); e != nil; e = e.Next() {
		cur := e.Value.(serializeAcquireReq)
		if cur.priority < req.priority || (cur.priority == req.priority && cur.seq > req.seq) {
			waiters.InsertBefore(req, e)
			return
		}
	}
	waiters.PushBack(req)
}

func bestSerializeWaiter(waiters *list.List) *list.Element {
	for e := waiters.Front(); e != nil; e = e.Next() {
		req := e.Value.(serializeAcquireReq)
		select {
		case <-req.done:
			waiters.Remove(e)
			continue
		default:
			return e
		}
	}
	return nil
}

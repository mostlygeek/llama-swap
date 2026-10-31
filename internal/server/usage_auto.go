package server

import (
	"time"

	"github.com/tidwall/gjson"
)

// autoExtractor is used when the upstream is unknown, and for llama-swap
// peers, which pass through whatever their own upstream returns. It reads
// the API usage block and then applies every server's extra fields it finds,
// in the order oMLX rates, TabbyAPI rates, llama-server timings (plus the
// kyojin draft counters kept in the same block), vLLM metrics, so a more
// precise source overrides a less precise one.
type autoExtractor struct{}

func (autoExtractor) newParser(path string, start time.Time) usageParser {
	return &autoParser{start: start, read: apiReaderFor(path), api: newAPIUsage()}
}

type autoParser struct {
	start   time.Time
	read    apiReader
	api     apiUsage
	block   gjson.Result // last usage block
	timings gjson.Result
	metrics gjson.Result
}

func (p *autoParser) event(ev gjson.Result) {
	ev = lastArrayElement(ev)
	p.read(ev, &p.api)
	if b := ev.Get("usage"); b.IsObject() {
		p.block = b
	}
	if t := ev.Get("timings"); t.IsObject() {
		p.timings = t
	}
	if m := ev.Get("metrics"); m.IsObject() {
		p.metrics = m
	}
}

func (p *autoParser) usage() Usage {
	u := p.api.toUsage(p.start)
	applyOMLXRates(&u, p.block)
	applyTabbyAPIRates(&u, p.block) // TabbyAPI's fields win when both appear
	applyTimings(&u, p.timings)
	applyKyojinDrafts(&u, p.timings)
	applyVLLMMetrics(&u, p.metrics)
	return u
}

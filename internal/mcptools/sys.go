package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SysProvider serves facts about the machine llama-swap is running on: the
// clock, and current system and GPU usage when a PerformanceReporter is
// given. The hardware snapshot (hw.HardwareSnapshot, served at /api/hardware)
// also belongs here; wiring it in is a change to this file.
type SysProvider struct {
	// now is injectable so tests can assert against a fixed instant.
	now  func() time.Time
	perf PerformanceReporter
}

// PerformanceReporter describes current system and GPU usage as text.
// perf.Monitor implements it. It is an interface because perf depends on
// config, which depends on this package.
type PerformanceReporter interface {
	PerformanceReport(ctx context.Context) string
}

// NewSysProvider builds the provider. A nil clock uses time.Now. A nil perf
// leaves out the performance tool.
func NewSysProvider(now func() time.Time, perf PerformanceReporter) *SysProvider {
	if now == nil {
		now = time.Now
	}
	return &SysProvider{now: now, perf: perf}
}

func (p *SysProvider) ID() string { return "sys" }

func (p *SysProvider) Shutdown(time.Duration) error { return nil }

func (p *SysProvider) Tools(context.Context) ([]Tool, error) {
	tools := []Tool{
		{
			Name:  "now",
			Title: "Current date and time",
			Description: "Get the current date and time on the machine running llama-swap, in UTC and in the " +
				"server's local timezone. Optionally also in a named timezone.",
			// Read-only, but deliberately not idempotent: the answer changing
			// is the entire point.
			Annotations: &Annotations{ReadOnlyHint: true, IdempotentHint: false},
			Tags:        []string{"time", "clock", "system"},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"timezone": map[string]any{
						"type": "string",
						"description": "An IANA timezone name such as 'America/Vancouver' or 'Asia/Tokyo'. " +
							"Omit for UTC and server local time only.",
					},
				},
				"additionalProperties": false,
			},
		},
	}
	if p.perf != nil {
		tools = append(tools, Tool{
			Name:  "performance",
			Title: "Current system and GPU usage",
			Description: "Get the newest CPU, memory, load and per-GPU readings (utilization, VRAM and shared " +
				"memory, temperature, fan, power) from llama-swap's performance monitor. GPUs are only read " +
				"while a model is loaded unless performance.gpu.poll is always; the result says when a " +
				"section was not sampled.",
			Annotations: &Annotations{ReadOnlyHint: true, IdempotentHint: false},
			Tags:        []string{"gpu", "vram", "cpu", "memory", "performance", "system"},
			InputSchema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		})
	}
	return tools, nil
}

func (p *SysProvider) Call(ctx context.Context, name string, args map[string]json.RawMessage) (Result, error) {
	switch name {
	case "now":
		return p.timeNow(args), nil
	case "performance":
		if p.perf == nil {
			return Result{}, fmt.Errorf("unknown tool %q", name)
		}
		return CapResult(p.perf.PerformanceReport(ctx), MaxToolResultBytes), nil
	default:
		return Result{}, fmt.Errorf("unknown tool %q", name)
	}
}

func (p *SysProvider) timeNow(args map[string]json.RawMessage) Result {
	now := p.now()

	var b strings.Builder
	writeZone(&b, "UTC", now.UTC())
	writeZone(&b, "server local", now.Local())

	if zone := StringArg(args, "timezone"); zone != "" {
		// LoadLocation reads the system zoneinfo database, which a minimal
		// container image may not carry. Reporting that as a tool error rather
		// than a protocol error lets the model fall back to UTC.
		loc, err := time.LoadLocation(zone)
		if err != nil {
			return Result{
				IsError: true,
				Content: fmt.Sprintf("Error: unknown timezone %q (%v). Use an IANA name such as "+
					"'America/Vancouver'. UTC and server local time are always available:\n\n%s", zone, err, b.String()),
			}
		}
		writeZone(&b, zone, now.In(loc))
	}

	fmt.Fprintf(&b, "unix: %d\n", now.Unix())
	return CapResult(b.String(), MaxToolResultBytes)
}

// writeZone renders one zone as RFC 3339 plus a human-readable form, with the
// offset spelled out so a model does not have to derive it.
func writeZone(b *strings.Builder, label string, t time.Time) {
	name, offset := t.Zone()
	fmt.Fprintf(b, "%s: %s (%s, UTC%+03d:%02d)\n",
		label,
		t.Format(time.RFC3339),
		name,
		offset/3600,
		(abs(offset)%3600)/60,
	)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

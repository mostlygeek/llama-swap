package router

import (
	"fmt"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

type Group struct {
	*baseRouter
}

func NewGroup(conf config.Config, proxylog, upstreamlog *logmon.Monitor) (*Group, error) {
	modelToGroup := make(map[string]string)
	for gid, gcfg := range conf.Groups {
		for _, mid := range gcfg.Members {
			if existing, dup := modelToGroup[mid]; dup {
				return nil, fmt.Errorf("model %q is in multiple groups: %q and %q", mid, existing, gid)
			}
			modelToGroup[mid] = gid
		}
	}

	planner := &groupPlanner{
		config:       conf,
		modelToGroup: modelToGroup,
	}

	processes := make(map[string]process.Process, len(modelToGroup))
	base := newBaseRouter("group", conf, processes, planner, proxylog)
	planner.processes = processes

	for mid := range modelToGroup {
		modelCfg, _, ok := conf.FindConfig(mid)
		if !ok {
			base.shutdownFn()
			return nil, fmt.Errorf("no model config for %q", mid)
		}
		procLog := logmon.NewWriter(upstreamlog)
		p, err := process.New(base.shutdownCtx, mid, modelCfg, procLog, proxylog)
		if err != nil {
			base.shutdownFn()
			return nil, fmt.Errorf("creating process for %q: %w", mid, err)
		}
		processes[mid] = p
	}

	g := &Group{baseRouter: base}
	go base.run()
	return g, nil
}

// groupPlanner decides evictions from static group configuration.
//
// Same-group siblings are stopped when the group has swap=true. Cross-group
// members are stopped only when the target's group is exclusive; loading a
// model from a non-exclusive group leaves running exclusive groups alone,
// matching the gotcha in the original ProcessGroup behaviour.
type groupPlanner struct {
	config       config.Config
	modelToGroup map[string]string
	processes    map[string]process.Process
}

func (p *groupPlanner) EvictionFor(target string) []string {
	tg := p.modelToGroup[target]
	tgCfg := p.config.Groups[tg]

	var result []string
	for mID, proc := range p.processes {
		if mID == target {
			continue
		}
		st := proc.State()
		if st == process.StateStopped || st == process.StateShutdown {
			continue
		}
		og := p.modelToGroup[mID]

		switch {
		case og == tg && tgCfg.Swap:
			result = append(result, mID)

		// the previous ProcessGroup behaviour did not unload exclusive groups
		// when loading a non-exclusive model. This maintains that gotcha
		// for backwards compatibility. The newer swap matrix approach does not
		// have this issue.
		case og != tg && tgCfg.Exclusive:
			result = append(result, mID)
		}
	}
	return result
}

func (p *groupPlanner) OnSwapStart(target string) {}

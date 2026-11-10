package playback

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"icetudio/internal/pluginhost"
)

func (s *Score) pluginMixer(ctx context.Context, offline, playing bool, startFrame int64) (*mixPlan, []*pluginhost.Instance, error) {
	if s.mix == nil {
		return nil, nil, nil
	}
	mix := *s.mix
	mix.nodes = slices.Clone(mix.nodes)
	mix.edges = slices.Clone(mix.edges)
	mix.issues = nil
	var workers []*pluginhost.Instance
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	open := func(f *mixFX, kind string) error {
		f.worker, f.latency = nil, 0
		if f.plugin == nil || !playing {
			return nil
		}
		p := f.plugin
		var err error
		if s.catalog == nil {
			err = fmt.Errorf("未配置本机插件库")
		} else {
			f.initial = make([]float64, len(f.parameters))
			var overrides []pluginhost.Parameter
			for i, v := range f.parameters {
				f.initial[i] = v.at(startFrame)
				if f.initial[i] != p.Parameters[i].Default {
					overrides = append(overrides, pluginhost.Parameter{ID: p.Parameters[i].ID, Value: f.initial[i]})
				}
			}
			f.worker, err = s.catalog.OpenWithParameters(ctx, pluginhost.Identity{Format: p.Format, Class: p.Class, SHA256: p.SHA256}, kind, p.State, offline, overrides)
		}
		if err != nil {
			err = fmt.Errorf("%s：%w", p.Name, err)
			mix.issues = append(mix.issues, err.Error())
			if offline {
				return err
			}
			return nil
		}
		workers = append(workers, f.worker)
		f.latency = f.worker.Latency
		if offline {
			f.latency += pluginhost.OfflineLead
		} else {
			f.latency += pluginhost.LiveLead
		}
		return nil
	}
	fail := func(err error) (*mixPlan, []*pluginhost.Instance, error) {
		closePlugins(workers)
		return nil, nil, err
	}
	for i := range mix.nodes {
		node := &mix.nodes[i]
		if node.instrument != nil {
			fx := *node.instrument
			node.instrument = &fx
			if err := open(&fx, "instrument"); err != nil {
				return fail(err)
			}
		}
		node.effects = slices.Clone(node.effects)
		for j := range node.effects {
			if err := open(&node.effects[j], "effect"); err != nil {
				return fail(err)
			}
		}
	}
	mix.compensate()
	frames := 0
	for _, n := range mix.nodes {
		frames += n.align + n.sourceLatency
		for _, f := range n.effects {
			frames += f.latency
			if f.kind == 2 {
				frames += n.outputLatency
			}
		}
	}
	for _, edge := range mix.edges {
		frames += edge.delay
	}
	if frames > 8<<20 {
		return fail(fmt.Errorf("混音延迟缓冲超过 64 MiB"))
	}
	return &mix, workers, nil
}

func closePlugins(workers []*pluginhost.Instance) {
	for _, worker := range workers {
		worker.Close()
	}
}

func (p mixParam) at(frame int64) float64 {
	i := sort.Search(len(p.points), func(i int) bool { return p.points[i].frame > frame })
	if i == 0 {
		return p.value
	}
	a := p.points[i-1]
	if i == len(p.points) || a.step {
		return a.value
	}
	b := p.points[i]
	return a.value + (b.value-a.value)*float64(frame-a.frame)/float64(b.frame-a.frame)
}

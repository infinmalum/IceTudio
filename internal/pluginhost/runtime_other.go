//go:build !linux

package pluginhost

import (
	"context"
	"fmt"
)

type Instance struct {
	Version, Vendor string
	Latency         int
	Parameters      []Parameter
	State           []byte
}

func (p *Instance) Close()     {}
func (p *Instance) Terminate() {}

func openInstance(context.Context, Module, Class, []byte, bool, []Parameter) (*Instance, error) {
	return nil, fmt.Errorf("此平台暂未提供 CLAP/VST3 播放")
}

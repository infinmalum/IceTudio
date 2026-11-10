package pluginhost

import (
	"context"
	"fmt"
)

const (
	MaxState      = 1 << 20
	MaxParameters = 512
	MaxLatency    = 48000
	LiveLead      = 512
	OfflineLead   = 256
)

// Identity contains no machine-local information. A scanned module is resolved
// by its exact class and fingerprint, including when a bundle moves machines.
type Identity struct {
	Format string `json:"format"`
	Class  string `json:"class"`
	SHA256 string `json:"sha256"`
}

type Parameter struct {
	ID    uint32  `json:"id"`
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

func (c *Catalog) Open(ctx context.Context, identity Identity, kind string, state []byte, offline bool) (*Instance, error) {
	return c.OpenWithParameters(ctx, identity, kind, state, offline, nil)
}

func (c *Catalog) OpenWithParameters(ctx context.Context, identity Identity, kind string, state []byte, offline bool, overrides []Parameter) (*Instance, error) {
	module, class, err := c.resolve(ctx, identity, kind)
	if err != nil {
		return nil, err
	}
	return openInstance(ctx, module, class, state, offline, overrides)
}

func (c *Catalog) resolve(ctx context.Context, identity Identity, kind string) (Module, Class, error) {
	items, err := c.List()
	if err != nil {
		return Module{}, Class{}, err
	}
	for _, module := range items {
		if module.Blocked || module.Status != Ready || module.Format != identity.Format || module.SHA256 != identity.SHA256 {
			continue
		}
		for _, class := range module.Classes {
			if class.ID == identity.Class && class.Kind == kind {
				hash, err := hashBinary(ctx, module.Path)
				if err != nil || hash != module.SHA256 {
					continue
				}
				return module, class, nil
			}
		}
	}
	return Module{}, Class{}, fmt.Errorf("插件未登记、已移动或二进制版本变化：%s", identity.Class)
}

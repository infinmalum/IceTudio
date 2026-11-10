//go:build !linux

package pluginhost

import (
	"context"
	"fmt"
)

type Editor struct{}

func (*Editor) Close()                {}
func (*Editor) Done() <-chan struct{} { return nil }
func (*Editor) Capture(context.Context) ([]byte, []Parameter, error) {
	return nil, nil, fmt.Errorf("此平台未提供插件窗口")
}
func (*Catalog) OpenEditor(context.Context, Identity, string, []byte, []Parameter) (*Editor, error) {
	return nil, fmt.Errorf("此平台未提供插件窗口")
}

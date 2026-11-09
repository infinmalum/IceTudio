//go:build !linux

package pluginhost

import (
	"context"
	"fmt"
	"os"
)

func prepareScanner(context.Context) (scanModule, error) {
	return nil, fmt.Errorf("此平台暂未提供 CLAP/VST3 隔离扫描")
}

func openRegular(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("插件或索引路径不是常规文件")
	}
	return os.Open(path)
}

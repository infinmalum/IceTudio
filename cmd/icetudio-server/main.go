// icetudio-server starts the internal, authenticated IPC host without a GUI.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"icetudio/internal/host"
	"icetudio/internal/ipc"
)

func main() {
	if len(os.Args) > 1 {
		fmt.Fprintln(os.Stderr, "icetudio-server 不接受命令行参数。")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	h, err := host.Start()
	if err != nil {
		return fmt.Errorf("宿主启动失败: %w", err)
	}
	defer h.Close()
	if h.InitError != nil {
		return fmt.Errorf("宿主初始化失败: %w", h.InitError)
	}
	server, err := ipc.Start(h.Service, h.DataDir())
	if err != nil {
		return fmt.Errorf("IPC 启动失败: %w", err)
	}
	defer server.Close()
	fmt.Println("IceTudio IPC 宿主已启动。")
	<-ctx.Done()
	return nil
}

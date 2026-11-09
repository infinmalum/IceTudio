package main

import (
	"fmt"
	"icetudio/internal/desktop"
	"icetudio/internal/host"
	"log"
	"os"
)

func main() {
	if len(os.Args) > 1 {
		fmt.Fprintln(os.Stderr, "IceTudio 桌面程序不接受命令行参数，请直接打开应用。")
		os.Exit(2)
	}
	runDesktop()
}
func runDesktop() {
	h, err := host.Start()
	if err != nil {
		log.Print(err)
		return
	}
	defer h.Close()
	if err := desktop.Run(h.Service, desktop.Options{StateDir: h.Paths.Data}); err != nil {
		log.Print(err)
	}
}

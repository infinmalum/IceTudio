package main

import (
	"icetudio/internal/cli"
	"icetudio/internal/desktop"
	"icetudio/internal/host"
	"log"
	"os"
)

func main() {
	if args := os.Args[1:]; len(args) > 0 {
		os.Exit(cli.Run(args))
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

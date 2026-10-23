package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"icetudio/internal/app"
	"icetudio/internal/cli"
	"icetudio/internal/host"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// With arguments this binary is the CLI: `icetudio serve` boots the
	// headless host (with IPC), everything else talks to the running host of
	// the data directory. Without arguments it is the desktop app.
	args := os.Args[1:]
	if len(args) > 0 {
		os.Exit(cli.Run(args))
	}
	runDesktop()
}

func runDesktop() {
	h, err := host.Start()
	if err != nil {
		log.Fatal(err)
	}
	defer h.Close()
	// GTK/WebKit uses XDG paths for its own persistent page state and cache.
	// Set them before creating the first webview so it stays with this install.
	if runtime.GOOS == "linux" {
		for key, subdir := range map[string]string{"XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "storage", "XDG_CACHE_HOME": "cache", "XDG_STATE_HOME": "state"} {
			dir := filepath.Join(h.Paths.Data, "webview", subdir)
			if err := os.MkdirAll(dir, 0700); err != nil {
				log.Fatal(err)
			}
			if err := os.Setenv(key, dir); err != nil {
				log.Fatal(err)
			}
		}
	}

	desktop := &DesktopService{}
	wails := application.New(application.Options{
		Name:        "IceTudio",
		Description: "Your local AI assistant",
		Services: []application.Service{
			application.NewService(desktop),
			application.NewService(&StatusService{svc: h.Service}),
			application.NewService(&ProjectService{svc: h.Service}),
			application.NewService(&PlaybackService{svc: h.Service}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Windows:    application.WindowsOptions{WebviewUserDataPath: filepath.Join(h.Paths.Data, "webview")},
		ShouldQuit: func() bool { return !desktop.blockClose(func() {}) },
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	desktop.emit = func(id string) { wails.Event.Emit("icetudio:close-requested", id) }
	desktop.quit = wails.Quit

	// Bridge post-commit service events (including streamed deltas) to the
	// webview; the frontend deduplicates by sequence when needed.
	stopEvents := h.Service.Subscribe(func(evt app.Event) {
		wails.Event.EmitEvent(&application.CustomEvent{Name: "icetudio:service-event", Data: evt})
	})
	defer stopEvents()

	window := wails.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "IceTudio",
		Width:            1100,
		Height:           720,
		MinWidth:         760,
		MinHeight:        520,
		BackgroundColour: application.NewRGB(245, 248, 250),
		URL:              "/",
	})
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		desktop.blockClose(event.Cancel)
	})

	if err := wails.Run(); err != nil {
		log.Fatal(err)
	}
}

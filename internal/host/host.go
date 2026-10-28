// Package host boots the single IceTudio host for its selected data directory:
// configuration, the ownership lock, the SQLite store, run recovery, the
// skills catalog, the MCP hub and the application service. The desktop
// entry point and the headless `icetudio serve` host share this path so both
// expose identical behavior to clients.
package host

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"icetudio/internal/agent"
	"icetudio/internal/app"
	"icetudio/internal/config"
	"icetudio/internal/mcptools"
	"icetudio/internal/model"
	"icetudio/internal/playback"
	"icetudio/internal/skills"
	"icetudio/internal/storage"
)

// Host holds everything a running IceTudio host owns. Close tears the
// process-owned resources down in reverse order.
type Host struct {
	Playback *playback.Manager
	Service  *app.Service
	Store    *storage.Store
	Runs     *agent.Runs
	Hub      *mcptools.Hub
	Config   config.Config
	Paths    config.Paths
	CredPath string
	Catalog  *skills.Catalog

	lock    *os.File
	logFile *os.File
	// InitError carries a non-fatal startup failure (the host stays
	// running but the UI/CLI should surface it).
	InitError error
}

// Start performs the full host startup for the installed executable. Startup
// failures are carried in InitError (the host still runs so clients and the
// UI can surface them); only catalog/MCP construction errors are fatal.
func Start() (*Host, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return nil, err
	}
	host := &Host{Paths: paths, CredPath: filepath.Join(paths.Data, "credentials.json")}
	var initErr error
	if err := config.EnsureDirs(paths); err != nil {
		initErr = err
	} else {
		logFile, err := os.OpenFile(filepath.Join(paths.Data, "icetudio.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			initErr = err
		} else {
			host.logFile = logFile
			log.SetOutput(logFile)
		}
	}
	if initErr == nil {
		host.Config, initErr = config.Load(paths.Config)
	}
	// One host per data directory: a second process must refuse clearly
	// instead of racing the first over the database and run state.
	if initErr == nil {
		host.lock, initErr = app.AcquireHostLock(paths.Data)
	}
	if initErr != nil {
		log.Printf("startup: %v", initErr)
	} else {
		store, err := storage.Open(filepath.Join(paths.Data, "icetudio.db"))
		if err != nil {
			log.Printf("startup: %v", err)
			initErr = err
		} else {
			host.Store = store
			if err := store.RecoverInterrupted(context.Background()); err != nil {
				log.Printf("恢复中断任务: %v", err)
			}
		}
	}
	host.InitError = initErr

	host.Runs = agent.NewRuns(host.Store)
	catalog, err := skills.Discover(filepath.Join(filepath.Dir(paths.Config), "skills"))
	if err != nil {
		host.Close()
		return nil, err
	}
	host.Catalog = catalog
	hub, err := mcptools.NewLocal(context.Background(), catalog)
	if err != nil {
		host.Close()
		return nil, err
	}
	host.Hub = hub
	if err := hub.Configure(host.Config.MCPServers); err != nil {
		host.Close()
		return nil, err
	}

	// The unified model client dispatches by provider: key-less local
	// compat servers and the three official remote SDKs, whose credentials
	// are resolved per request from the protected credential store.
	modelClient := model.Client{Keys: config.FileCredentialLookup(host.CredPath)}
	host.Playback = playback.New(paths.Data, func(id string) (playback.Snapshot, error) {
		if host.Store == nil {
			return playback.Snapshot{}, fmt.Errorf("工程存储未就绪")
		}
		item, err := host.Store.GetProject(context.Background(), id)
		return playback.Snapshot{ID: item.ID, Revision: item.Revision, State: item.State}, err
	})
	host.Service = app.New(app.Options{
		Playback: host.Playback, Store: host.Store, Runs: host.Runs, Tools: hub, Skills: catalog,
		Chat: modelClient, Discovery: modelClient, Config: host.Config, ConfigPath: paths.Config,
		DataDir: paths.Data, MCPEndpoints: hub, MCPManager: hub, InitError: host.InitError, CredentialPath: host.CredPath,
	})
	if host.Store != nil {
		if err := host.Service.SyncMCPEndpoints(context.Background()); err != nil {
			log.Printf("MCP 指纹登记: %v", err)
		}
	}
	return host, nil
}

// Close releases the host's resources. A nil host or a host that failed
// during startup closes only what was actually acquired.
func (h *Host) Close() {
	if h == nil {
		return
	}
	if h.Playback != nil {
		h.Playback.Close()
		h.Playback = nil
	}
	if h.Hub != nil {
		h.Hub.Close()
		h.Hub = nil
	}
	if h.Runs != nil {
		h.Runs.Close()
		h.Runs = nil
	}
	if h.Store != nil {
		h.Store.Close()
		h.Store = nil
	}
	if h.lock != nil {
		h.lock.Close()
		h.lock = nil
	}
	if h.logFile != nil {
		h.logFile.Close()
		h.logFile = nil
	}
}

// DataDir returns the host's protected data directory.
func (h *Host) DataDir() string { return h.Paths.Data }

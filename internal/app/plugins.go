package app

import (
	"context"

	"icetudio/internal/pluginhost"
)

// PluginCatalog is local desktop inventory. No project, CLI or Agent edit can
// authorize plugin execution through these methods.
func (s *Service) PluginCatalog() ([]pluginhost.Module, error) {
	return s.plugins.List()
}

func (s *Service) ScanPluginFolder(ctx context.Context, folder string, progress func(int, int)) error {
	return s.plugins.Scan(ctx, folder, progress)
}

func (s *Service) RetryPluginScan(ctx context.Context, path string) error {
	return s.plugins.Retry(ctx, path)
}

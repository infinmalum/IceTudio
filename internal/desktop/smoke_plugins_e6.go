package desktop

import (
	"fmt"
	"slices"

	"icetudio/internal/pluginhost"
)

func (a *Workspace) verifyPluginCatalogE6(folder string) error {
	before := a.preview.Context()
	if err := a.onMain(a.openPluginCatalog); err != nil {
		return err
	}
	ready := func(check func() bool) error {
		return waitCondition(func() bool {
			var ok bool
			_ = a.onMain(func() { ok = a.pluginShelf.open && !a.pluginShelf.busy && check() })
			return ok
		})
	}
	if err := ready(func() bool { return true }); err != nil {
		return err
	}
	if err := a.onMain(func() { a.runPluginCatalog(folder, false) }); err != nil {
		return err
	}
	if err := ready(func() bool {
		return slices.ContainsFunc(a.pluginShelf.modules, func(m pluginhost.Module) bool {
			return m.Status == pluginhost.Ready && m.Format == "clap"
		}) && slices.ContainsFunc(a.pluginShelf.modules, func(m pluginhost.Module) bool {
			return m.Status == pluginhost.Ready && m.Format == "vst3"
		})
	}); err != nil {
		return fmt.Errorf("未在 Native 插件库看到两种格式：%w", err)
	}
	after := a.preview.Context()
	if before.ProjectID != after.ProjectID || before.Revision != after.Revision {
		return fmt.Errorf("扫描改变了工程修订")
	}
	return a.onMain(func() { a.pluginShelf.open = false })
}

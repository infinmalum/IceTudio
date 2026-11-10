package desktop

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"icetudio/internal/pluginhost"
)

type pluginShelf struct {
	open, busy        bool
	folder, query     string
	message           string
	modules           []pluginhost.Module
	page, done, total int
	cancel            context.CancelFunc
}

func (a *Workspace) openPluginCatalog() {
	if a.service == nil || a.closeFrozen || a.form != nil {
		return
	}
	p := &a.pluginShelf
	p.open = true
	if !p.busy {
		a.runPluginCatalog("", false)
	}
}

func (a *Workspace) choosePluginFolder() {
	p := &a.pluginShelf
	if p.busy || a.closeFrozen {
		return
	}
	p.busy = true
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: a.win, Title: "选择要扫描的 CLAP / VST3 插件目录", Directory: true,
		})
		a.update(func() {
			p.busy = false
			if err != nil {
				p.message = err.Error()
			} else if len(paths) > 0 {
				p.folder = paths[0]
			}
		})
	}()
}

func (a *Workspace) cancelPluginScan() {
	if cancel := a.pluginShelf.cancel; cancel != nil {
		cancel()
	}
}

func (a *Workspace) runPluginCatalog(path string, retry bool) {
	p := &a.pluginShelf
	if p.busy || a.closeFrozen {
		return
	}
	p.busy, p.done, p.total = true, 0, 0
	p.message = ""
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go func() {
		defer cancel()
		var err error
		if retry {
			err = a.service.RetryPluginScan(ctx, path)
		} else if path != "" {
			err = a.service.ScanPluginFolder(ctx, path, func(done, total int) {
				a.update(func() { p.done, p.total = done, total })
			})
		}
		modules, loadErr := a.service.PluginCatalog()
		a.update(func() {
			p.busy, p.cancel = false, nil
			if loadErr == nil {
				p.modules = modules
			}
			switch {
			case loadErr != nil:
				p.message = loadErr.Error()
			case errors.Is(err, context.Canceled):
				p.message = "扫描已取消，已完成的结果已保留"
			case err != nil:
				p.message = err.Error()
			case path != "":
				p.message = "扫描完成，失败和中断的模块可单独重试"
			}
		})
	}()
}

func (a *Workspace) pluginCatalogDialog(c *ui.Context) {
	p := &a.pluginShelf
	if !p.open || a.closeFrozen {
		return
	}
	width, height := c.Size()
	ui.Modal(c, &p.open, func() {
		ui.Scroll(c).Width(min(600, width-64)).MaxHeight(height - 80).Children(func() {
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				ui.Text(c, "音频插件库").FontSize(18).Bold()
				ui.Text(c, "扫描会在隔离进程中加载目录里的 CLAP 和 VST3。请选择信任的插件目录。").TextColor(c.Theme().TextMuted)
				ui.Text(c, "扫描成功后，在混音台的通道或效果页挂载插件。").TextColor(c.Theme().TextMuted)
				ui.Row(c).FillWidth().Wrap().Gap(6).Children(func() {
					ui.Button(c, "选择目录").Disabled(p.busy).OnClick(a.choosePluginFolder)
					ui.PrimaryButton(c, "扫描所选目录").Disabled(p.busy || p.folder == "").OnClick(func() { a.runPluginCatalog(p.folder, false) })
					ui.Button(c, "取消扫描").Disabled(p.cancel == nil).OnClick(a.cancelPluginScan)
					ui.Box(c).Grow(1)
					ui.Button(c, "返回").OnClick(func() { p.open = false })
				})
				if p.folder != "" {
					ui.Text(c, p.folder).MaxWidth(580).TextColor(c.Theme().TextMuted)
				}
				if p.busy {
					if p.total > 0 {
						ui.Textf(c, "正在检查模块 %d / %d", min(p.done+1, p.total), p.total)
					} else {
						ui.Text(c, "正在处理插件目录…")
					}
				}
				if p.message != "" {
					ui.Text(c, p.message).MaxWidth(580).TextColor(c.Theme().TextMuted)
				}
				ui.TextInput(c, &p.query).Label("搜索插件名称、厂商或路径").Placeholder("搜索插件").OnChange(func() { p.page = 0 })
				a.pluginCatalogResults(c, p)
			})
		})
	})
}

func (a *Workspace) pluginCatalogResults(c *ui.Context, p *pluginShelf) {
	query := strings.ToLower(strings.TrimSpace(p.query))
	var matches []pluginhost.Module
	for _, module := range p.modules {
		if matchesPlugin(module, query) {
			matches = append(matches, module)
		}
	}
	const pageSize = 16
	lastPage := max(0, (len(matches)-1)/pageSize)
	p.page = min(p.page, lastPage)
	first := p.page * pageSize
	ui.Scroll(c).MaxHeight(325).Children(func() {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			if len(matches) == 0 {
				ui.Text(c, "没有符合条件的插件模块")
			}
			for _, module := range matches[first:min(first+pageSize, len(matches))] {
				a.pluginModuleRow(c.Key(module.Path), module, p.busy)
			}
		})
	})
	ui.Row(c).Gap(6).Children(func() {
		ui.Textf(c, "%d 个模块 · 第 %d / %d 页", len(matches), p.page+1, lastPage+1).Grow(1)
		ui.Button(c, "上一页").Disabled(p.page == 0).OnClick(func() { p.page-- })
		ui.Button(c, "下一页").Disabled(p.page >= lastPage).OnClick(func() { p.page++ })
	})
}

func matchesPlugin(module pluginhost.Module, query string) bool {
	if strings.Contains(strings.ToLower(module.Path+" "+module.Format), query) {
		return true
	}
	for _, class := range module.Classes {
		if strings.Contains(strings.ToLower(class.Name+" "+class.Vendor), query) {
			return true
		}
	}
	return false
}

func (a *Workspace) pluginModuleRow(c *ui.Context, module pluginhost.Module, busy bool) {
	ui.Column(c).Gap(2).Padding(6).Background(c.Theme().Surface).Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(5).Children(func() {
			ui.Text(c, fmt.Sprintf("%s · %s", filepath.Base(module.Path), strings.ToUpper(module.Format))).MinWidth(110).Grow(1).SingleLine().Bold()
			retry := "重新扫描"
			if module.Status != pluginhost.Ready {
				retry = "重试扫描"
			}
			label := "停用模块"
			if module.Blocked {
				label = "重新启用"
			}
			ui.Button(c, label).Disabled(busy).OnClick(func() { a.togglePluginModule(module) })
			ui.Button(c, retry).Disabled(busy || module.Blocked).OnClick(func() { a.runPluginCatalog(module.Path, true) })
		})
		if module.Blocked {
			ui.Text(c, "已停用，保留现有工程槽位").TextColor(c.Theme().Warning)
		}
		ui.Text(c, module.Path).MaxWidth(565).FontSize(11).TextColor(c.Theme().TextMuted)
		switch module.Status {
		case pluginhost.Ready:
			for _, class := range module.Classes {
				kind := map[string]string{"instrument": "乐器", "effect": "效果", "other": "其他"}[class.Kind]
				ui.Text(c, strings.TrimSpace(class.Name+" · "+kind+"  "+class.Vendor+" "+class.Version)).MaxWidth(565)
			}
		case pluginhost.Interrupted:
			ui.Text(c, "上次扫描中断。可在确认模块可用后重试。").TextColor(c.Theme().Warning)
		case pluginhost.Failed:
			ui.Text(c, module.Detail).MaxWidth(565).TextColor(c.Theme().Warning)
		}
	})
}

func (a *Workspace) togglePluginModule(m pluginhost.Module) {
	p := &a.pluginShelf
	if p.busy || a.closeFrozen {
		return
	}
	p.busy = true
	go func() {
		err := a.service.SetPluginBlocked(m.Path, !m.Blocked)
		modules, readErr := a.service.PluginCatalog()
		a.update(func() {
			p.busy = false
			if err != nil {
				p.message = err.Error()
				return
			}
			if readErr != nil {
				p.message = readErr.Error()
				return
			}
			p.modules = modules
			if m.Blocked {
				p.message = "插件已重新启用"
			} else {
				p.message = "插件已停用，工程状态保留"
			}
		})
	}()
}

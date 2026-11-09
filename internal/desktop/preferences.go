package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

type nativePreferences struct {
	QuantizeKey      string  `json:"quantizeKey,omitempty"`
	DuplicateKey     string  `json:"duplicateKey,omitempty"`
	View             string  `json:"view,omitempty"`
	HideBrowser      bool    `json:"hideBrowser,omitempty"`
	HideAgent        bool    `json:"hideAgent,omitempty"`
	Follow           string  `json:"follow,omitempty"`
	Version          int     `json:"version"`
	Theme            string  `json:"theme"`
	Scale            float32 `json:"scale"`
	BrowserWidth     float32 `json:"browserWidth"`
	EditorShare      float32 `json:"editorShare"`
	Split            float32 `json:"split"`
	LastProject      string  `json:"lastProject"`
	LastConversation string  `json:"lastConversation"`
}

func defaultPreferences() nativePreferences {
	return nativePreferences{Version: 1, Theme: "dark", Scale: 1, BrowserWidth: 208, EditorShare: 72, Split: 48}
}
func validPreferences(p nativePreferences) bool {
	return validEditorKeys(p) && (p.View == "" || p.View == "split" || p.View == "arrangement" || p.View == "piano" || p.View == "mixer") && (p.Follow == "" || p.Follow == "关闭" || p.Follow == "页跟随" || p.Follow == "连续跟随") && p.Version == 1 && (p.Theme == "dark" || p.Theme == "light") && p.Scale >= .5 && p.Scale <= 2 && p.BrowserWidth >= 160 && p.BrowserWidth <= 800 && p.EditorShare >= 15 && p.EditorShare <= 90 && p.Split >= 10 && p.Split <= 90
}
func readPreferences(dir string) (nativePreferences, error) {
	p := defaultPreferences()
	if dir == "" {
		return p, nil
	}
	path := filepath.Join(dir, "native-ui.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16384 {
		return p, errors.New("Native 外观文件类型或大小无效，原文件保留")
	}
	f, err := os.Open(path)
	if err != nil {
		return p, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 16385))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return defaultPreferences(), err
	}
	if dec.Decode(new(any)) != io.EOF || !validPreferences(p) {
		return defaultPreferences(), errors.New("Native 外观文件版本或内容无效，原文件保留")
	}
	return p, nil
}
func writePreferences(dir string, p nativePreferences) error {
	if dir == "" {
		return nil
	}
	if !validPreferences(p) {
		return errors.New("外观设置超出支持范围")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, _ := json.Marshal(p)
	f, err := os.CreateTemp(dir, ".native-ui-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	path := filepath.Join(dir, "native-ui.json")
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (a *Workspace) savePreferences() {
	a.preferences.BrowserWidth = max(160, min(800, a.preferences.BrowserWidth))
	a.preferences.EditorShare = max(15, min(90, a.preferences.EditorShare))
	a.split = max(10, min(90, a.split))
	a.prefsVersion++
	if a.prefsSaving || a.stateDir == "" || a.draftQueue == nil || a.prefsError != "" {
		return
	}
	a.persistPreferences()
}
func (a *Workspace) persistPreferences() {
	p, version := a.preferences, a.prefsVersion
	p.Split = a.split
	a.prefsSaving = true
	a.draftQueue <- func() {
		err := writePreferences(a.stateDir, p)
		a.update(func() {
			a.prefsSaving = false
			if err != nil {
				a.prefsError = err.Error()
				return
			}
			if version != a.prefsVersion {
				a.persistPreferences()
			}
		})
	}
}
func (a *Workspace) appearanceSettings(c *ui.Context, d *nativeSettings) {
	ui.Text(c, "外观与布局").Bold()
	nativeSelect(c, "主题", &a.preferences.Theme, []string{"dark", "light"}, d.busy, func() { a.savePreferences(); a.sendPianoCommand("appearance", 0) })
	ui.Text(c, "界面缩放（Native 与嵌入内容）").FontSize(11)
	ui.Row(c).Gap(6).Children(func() {
		for _, scale := range []float32{.9, 1, 1.1, 1.25, 1.4} {
			ui.Button(c, fmt.Sprintf("%.0f%%", scale*100)).OnClick(func() { a.cancelArrangement(); a.preferences.Scale = scale; a.savePreferences() })
		}
	})
	ui.Text(c, "拖动工作区两条竖分隔线调整侧栏；拖动横分隔线调整编排与卷帘。恢复默认会重置主题、缩放和布局，保留上次工程与对话。").MaxWidth(630)
	ui.Button(c, "恢复默认外观").OnClick(func() {
		p := defaultPreferences()
		p.LastProject = a.preferences.LastProject
		p.LastConversation = a.preferences.LastConversation
		a.preferences = p
		a.split = p.Split
		a.savePreferences()
	})
	if a.prefsError != "" {
		ui.Text(c, "外观保存状态未知："+a.prefsError).MaxWidth(630).TextColor(c.Theme().Warning)
	}
	ui.Text(c, "工具快捷键（仅音乐画布焦点时，标准文字快捷键保留）").FontSize(12)
	if a.preferences.QuantizeKey == "" {
		a.preferences.QuantizeKey = "Q"
	}
	if a.preferences.DuplicateKey == "" {
		a.preferences.DuplicateKey = "B"
	}
	for _, item := range []struct {
		label string
		key   *string
	}{{"量化快捷键", &a.preferences.QuantizeKey}, {"紧邻复制快捷键", &a.preferences.DuplicateKey}} {
		previous := *item.key
		ui.Text(c, item.label).FontSize(11)
		ui.Select(c, item.key, []string{"Q", "W", "E", "R", "T", "B", "L"}).Width(160).Label(item.label).OnChange(func() {
			if !validEditorKeys(a.preferences) {
				*item.key = previous
				d.err = "工具快捷键冲突，请为两个动作选择不同按键"
				return
			}
			d.err = ""
			a.savePreferences()
			a.sendPianoCommand("keymap", 0)
		})
	}
	ui.Button(c, "恢复旧浏览器输入与偏好").Disabled(a.composerBlocked()).OnClick(func() { d.open = false; a.openBrowserRecovery() })
}
func preferencesFromBrowser(current nativePreferences, p map[string]string) nativePreferences {
	if theme := p["icetudio.theme"]; theme == "light" || theme == "dark" {
		current.Theme = theme
	}
	if f, err := strconv.ParseFloat(p["icetudio.ui-scale"], 32); err == nil && f >= .5 && f <= 2 {
		current.Scale = float32(f)
	}
	if f, err := strconv.ParseFloat(strings.TrimSuffix(p["icetudio.browser-width"], "px"), 32); err == nil && f >= 160 && f <= 800 {
		current.BrowserWidth = float32(f)
	}
	if f, err := strconv.ParseFloat(strings.TrimSuffix(p["icetudio.dock-width"], "px"), 32); err == nil && f >= 160 && f <= 800 {
		share := 100 - f/1232*100
		if share >= 15 && share <= 90 {
			current.EditorShare = float32(share)
		}
	}
	current.LastProject = p["icetudio.lastProject"]
	current.LastConversation = p["icetudio.lastConversation"]
	return current
}

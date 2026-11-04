package desktop

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/mattn/go-sqlite3"
	"icetudio/internal/app"
	"icetudio/internal/materials"
	"icetudio/internal/project"
)

//go:embed browser_export.py
var browserExportScript []byte

type browserDraftExport struct {
	Version     int                           `json:"version"`
	Text        map[string]string             `json:"text"`
	Attachments map[string][]materials.Upload `json:"attachments"`
	Preferences map[string]string             `json:"preferences"`
}
type browserRecovery struct {
	open, busy         bool
	data               *browserDraftExport
	err, message, path string
}

func copyBrowserProfile(source, dest string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("浏览器 profile 包含符号链接，已保留原目录，拒绝跟随")
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if strings.HasSuffix(path, "-wal") || strings.HasSuffix(path, "-shm") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("浏览器 profile 存在非普通文件；原目录未修改")
		}
		// SQLite Online Backup includes committed WAL data without modifying the
		// source. Blob files are copied unchanged; the old host must be stopped.
		if strings.HasSuffix(path, ".sqlite3") || strings.HasSuffix(path, ".sqlite") {
			return backupBrowserSQLite(path, target)

		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = io.Copy(dst, io.LimitReader(src, 128<<20))
		closeErr := dst.Close()
		if err != nil {
			return err
		}
		if info.Size() > 128<<20 {
			return errors.New("单个 profile 文件超过快照上限，原目录未修改")
		}
		if closeErr != nil {
			return closeErr
		}
		return os.Chtimes(target, info.ModTime(), info.ModTime())
	})
}
func readBrowserExport(ctx context.Context, profile string) (*browserDraftExport, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("当前自动接管器针对 Linux WebKit；可导入显式导出的草稿文件，原 profile 保留")
	}
	snapshot, err := os.MkdirTemp("", "icetudio-browser-snapshot-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(snapshot)
	source := filepath.Join(profile, "webview", "storage", "icetudio")
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		return &browserDraftExport{Version: 1, Text: map[string]string{}, Attachments: map[string][]materials.Upload{}, Preferences: map[string]string{}}, nil
	}
	copyPath := filepath.Join(snapshot, "profile")
	if err := copyBrowserProfile(source, copyPath); err != nil {
		return nil, err
	}
	script := filepath.Join(snapshot, "read.py")
	if err := os.WriteFile(script, browserExportScript, 0600); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/python3", script, copyPath, filepath.Join(snapshot, "cache"))
	var output, stderr bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("旧浏览器草稿读取失败；原目录完整保留：%w；%s", err, stderr.String())
	}
	return decodeBrowserExport(output.Bytes())
}
func decodeBrowserExport(b []byte) (*browserDraftExport, error) {
	if len(b) > 96<<20 {
		return nil, errors.New("草稿导出文件超过上限")
	}
	var out browserDraftExport
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return nil, err
	}
	if d.Decode(new(any)) != io.EOF || out.Version != 1 {
		return nil, errors.New("不支持的草稿导出版本或尾随内容")
	}
	return &out, nil
}
func importBrowserDrafts(svc *app.Service, data *browserDraftExport) (int, error) {
	if data == nil || data.Version != 1 {
		return 0, errors.New("不支持的草稿导出")
	}
	existing, err := svc.GetConversationDrafts()
	if err != nil {
		return 0, err
	}
	keys := map[string]bool{}
	for key := range data.Text {
		keys[key] = true
	}
	for key := range data.Attachments {
		keys[key] = true
	}
	count := 0
	for key := range keys {
		text, uploads := data.Text[key], data.Attachments[key]
		if text == "" && len(uploads) == 0 {
			continue
		}
		if current := existing[key]; current.Text != "" || len(current.Uploads) > 0 || current.Dispatching {
			if !current.Dispatching && current.Text == text && (reflect.DeepEqual(current.Uploads, uploads) || len(current.Uploads) == 0 && len(uploads) == 0) {
				continue
			}
			return count, fmt.Errorf("对话 %s 已有 Native 输入；源记录与导出文件保留，未覆盖。请处理冲突后再导入", key)
		}
		next := app.ConversationDraft{Token: project.NewID("importdraft"), Text: text, Uploads: uploads}
		if err := svc.SaveConversationDraft(key, existing[key].Token, next); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
func (a *Workspace) openBrowserRecovery() { a.browserRecovery = &browserRecovery{open: true} }
func (a *Workspace) browserRecoveryDialog(c *ui.Context) {
	d := a.browserRecovery
	if d == nil || !d.open || a.closeFrozen {
		return
	}
	ui.Modal(c, &d.open, func() {
		ui.Text(c, "恢复旧浏览器输入").FontSize(18).Bold()
		ui.Text(c, "先关闭旧应用。本工具只复制旧 profile 的快照，用本机 WebKit 读取文字与 IndexedDB 附件；原目录不会删除或清除。导入不覆盖已有 Native 输入。导出文件含私密输入，请妥善保管。").MaxWidth(700)
		ui.Button(c, "读取本应用目录中的旧输入").Disabled(d.busy || a.stateDir == "").OnClick(func() {
			d.busy = true
			dataDir := a.stateDir
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
				defer cancel()
				data, err := readBrowserExport(ctx, dataDir)
				a.update(func() {
					d.busy = false
					if err != nil {
						d.err = err.Error()
						return
					}
					d.data = data
					d.err = ""
					d.message = fmt.Sprintf("已读取 %d 项文字、%d 组附件。尚未导入。", len(data.Text), len(data.Attachments))
				})
			}()
		})
		ui.Button(c, "读取草稿导出文件").Disabled(d.busy).OnClick(func() {
			d.busy = true
			go func() {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "读取草稿导出文件"})
				var data *browserDraftExport
				if err == nil && len(paths) > 0 {
					var b []byte
					b, err = os.ReadFile(paths[0])
					if err == nil {
						data, err = decodeBrowserExport(b)
					}
				}
				a.update(func() {
					d.busy = false
					if err != nil {
						d.err = err.Error()
						return
					}
					if data != nil {
						d.data = data
						d.message = "已读取，尚未导入"
					}
				})
			}()
		})
		if d.data != nil {
			ui.Button(c, "应用旧外观与上次选区偏好").Disabled(d.busy || !a.projectAvailable()).OnClick(func() {
				a.preferences = preferencesFromBrowser(a.preferences, d.data.Preferences)
				a.split = a.preferences.Split
				a.savePreferences()
				d.message = "外观偏好已应用；上次工程/对话在下次启动定位，不改变在途任务"
			})
			ui.Button(c, "保存私有恢复副本").Disabled(d.busy).OnClick(func() {
				data := d.data
				d.busy = true
				go func() {
					path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: "保存私有草稿恢复副本", DefaultPath: "icetudio-input-recovery.json"})
					if err == nil && path != "" {
						b, e := json.Marshal(data)
						err = e
						if err == nil {
							err = os.WriteFile(path, b, 0600)
						}
					}
					a.update(func() {
						d.busy = false
						if err != nil {
							d.err = err.Error()
						} else {
							d.path = path
							d.message = "恢复副本已保存，原 profile 保留"
						}
					})
				}()
			})
			ui.Button(c, "导入到 Native 输入（不覆盖冲突）").Disabled(d.busy || a.composerBlocked()).OnClick(func() {
				data := d.data
				d.busy = true
				a.composer.busy = true
				a.draftQueue <- func() {
					count, err := importBrowserDrafts(a.service, data)
					a.update(func() {
						d.busy = false
						a.composer.busy = false
						d.message = fmt.Sprintf("已导入 %d 项；没有自动发送。原记录保留。", count)
						if err != nil {
							d.err = err.Error()
						}
						a.loadComposer()
					})
				}
			})
		}
		if d.message != "" {
			ui.Text(c, d.message).MaxWidth(700)
		}
		if d.err != "" {
			ui.Text(c, d.err).MaxWidth(700).TextColor(c.Theme().Warning)
		}
		ui.Button(c, "返回工作区").Disabled(d.busy).OnClick(func() { d.open = false })
	})
}

// Page backup does not rebuild WebKit's tables or require its private IDBKEY
// collation. It includes committed WAL pages and never writes the source.
func backupBrowserSQLite(source, target string) error {
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(source), RawQuery: "mode=ro"}).String()
	src, err := sql.Open("sqlite3", uri)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := sql.Open("sqlite3", target)
	if err != nil {
		return err
	}
	defer dst.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sc, err := src.Conn(ctx)
	if err != nil {
		return err
	}
	defer sc.Close()
	dc, err := dst.Conn(ctx)
	if err != nil {
		return err
	}
	defer dc.Close()
	return sc.Raw(func(s any) error {
		return dc.Raw(func(d any) error {
			copy, err := d.(*sqlite3.SQLiteConn).Backup("main", s.(*sqlite3.SQLiteConn), "main")
			if err != nil {
				return err
			}
			defer copy.Finish()
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				done, err := copy.Step(256)
				if err != nil {
					return err
				}
				if done {
					return nil
				}
				time.Sleep(time.Millisecond)
			}
		})
	})
}

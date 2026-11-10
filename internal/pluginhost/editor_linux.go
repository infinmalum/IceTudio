//go:build linux

package pluginhost

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

type editorDisplay struct {
	name, auth, socket string
	dir                string
	cmd                *exec.Cmd
	done               chan struct{}
}

func (d *editorDisplay) close() {
	if d.cmd != nil {
		_ = d.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-d.done:
		case <-time.After(time.Second):
			_ = d.cmd.Process.Kill()
			<-d.done
		}
	}
	if d.dir != "" {
		_ = os.RemoveAll(d.dir)
	}
}

func startEditorDisplay(ctx context.Context) (_ *editorDisplay, err error) {
	xephyr, err := exec.LookPath("Xephyr")
	if err != nil || os.Getenv("DISPLAY") == "" {
		return nil, fmt.Errorf("插件窗口需要 Linux X11/Xwayland 会话与 Xephyr。仍可使用 Native 参数面板。")
	}
	dir, err := os.MkdirTemp("", "icetudio-plugin-display-")
	if err != nil {
		return nil, err
	}
	d := &editorDisplay{dir: dir}
	defer func() {
		if err != nil {
			d.close()
		}
	}()
	var cookie [16]byte
	if _, err = rand.Read(cookie[:]); err != nil {
		return nil, err
	}
	number := strconv.Itoa(1000 + int(binary.BigEndian.Uint16(cookie[:2]))%29000)
	d.name = ":" + number
	d.socket = "/tmp/.X11-unix/X" + number
	d.auth = filepath.Join(dir, "authority")
	// Xauthority FamilyWild, address, display number, protocol and cookie.
	data := []byte{0xff, 0xff}
	for _, field := range [][]byte{nil, []byte(number), []byte("MIT-MAGIC-COOKIE-1"), cookie[:]} {
		data = binary.BigEndian.AppendUint16(data, uint16(len(field)))
		data = append(data, field...)
	}
	if err = os.WriteFile(d.auth, data, 0600); err != nil {
		return nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	defer w.Close()
	cmd := exec.CommandContext(ctx, xephyr, d.name, "-displayfd", "3", "-auth", d.auth,
		"-nolisten", "tcp", "-noreset", "-resizeable", "-screen", "1280x800")
	cmd.ExtraFiles = []*os.File{w}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	w.Close()
	d.cmd, d.done = cmd, make(chan struct{})
	go func() { _ = cmd.Wait(); close(d.done) }()
	if err = r.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return nil, err
	}
	var ready [32]byte
	n, err := r.Read(ready[:])
	if err != nil || strings.TrimSpace(string(ready[:n])) != number {
		return nil, fmt.Errorf("无法启动独立 Xephyr 插件窗口")
	}
	info, err := os.Lstat(d.socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, fmt.Errorf("独立插件显示套接字不可用")
	}
	return d, nil
}

type Editor struct {
	instance *Instance
	display  *editorDisplay
}

func (e *Editor) Close() {
	e.instance.Close()
	e.display.close()
}

func (e *Editor) Done() <-chan struct{} { return e.instance.done }

// Capture closes the plugin view after obtaining one state checkpoint. The
// caller still has to commit the revision-checked mixer draft.
func (e *Editor) Capture(ctx context.Context) ([]byte, []Parameter, error) {
	p := e.instance
	atomic.StoreUint32((*uint32)(unsafe.Pointer(&p.mapping[8])), 1)
	deadline := time.NewTimer(6 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for {
		if atomic.LoadUint32((*uint32)(unsafe.Pointer(&p.mapping[4]))) == 3 {
			if err := p.metadata(); err != nil {
				return nil, nil, err
			}
			return p.State, p.Parameters, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-p.done:
			return nil, nil, fmt.Errorf("插件窗口已关闭或未能保存状态")
		case <-deadline.C:
			return nil, nil, fmt.Errorf("插件窗口保存状态超时")
		case <-tick.C:
		}
	}
}

func (c *Catalog) OpenEditor(ctx context.Context, identity Identity, kind string, state []byte, overrides []Parameter) (*Editor, error) {
	module, class, err := c.resolve(ctx, identity, kind)
	if err != nil {
		return nil, err
	}
	app, err := os.Executable()
	if err != nil {
		return nil, err
	}
	display, err := startEditorDisplay(ctx)
	if err != nil {
		return nil, err
	}
	worker, err := startInstance(ctx, filepath.Join(filepath.Dir(app), "icetudio-plugin-run"), module, class, state, false, display, overrides)
	if err != nil {
		display.close()
		return nil, err
	}
	return &Editor{instance: worker, display: display}, nil
}

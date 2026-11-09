//go:build linux

package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const scanTimeout = 6 * time.Second

func openRegular(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("插件或索引路径不是常规文件")
	}
	return f, nil
}

type scanner struct {
	helper string
	bwrap  string
	limit  string
}

func prepareScanner(ctx context.Context) (scanModule, error) {
	app, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return prepareScannerAt(ctx, filepath.Join(filepath.Dir(app), "icetudio-plugin-scan"))
}

func prepareScannerAt(ctx context.Context, helper string) (scanModule, error) {
	info, err := os.Lstat(helper)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return nil, fmt.Errorf("插件扫描助手缺失或不可执行，请安装与 IceTudio 同目录的 icetudio-plugin-scan")
	}
	s := scanner{helper: helper, bwrap: "/usr/bin/bwrap", limit: "/usr/bin/prlimit"}
	for _, path := range []string{s.bwrap, s.limit} {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return nil, fmt.Errorf("隔离扫描需要系统命令 %s", filepath.Base(path))
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()
	out, diagnostic, err := s.invoke(probeCtx, Module{})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("无法启动插件隔离进程：%s", clean(errors.Join(err, textError(diagnostic)).Error()))
	}
	if string(out) != protocol {
		return nil, fmt.Errorf("插件扫描助手版本与 IceTudio 不匹配")
	}
	return s.scan, nil
}

func textError(s string) error {
	if s == "" {
		return nil
	}
	return errors.New(s)
}

func (s scanner) scan(ctx context.Context, module Module) ([]Class, string, error) {
	job, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()
	data, diagnostic, err := s.invoke(job, module)
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}
	if job.Err() != nil {
		return nil, "插件扫描超时，已停止隔离进程", nil
	}
	if err != nil {
		if strings.HasPrefix(strings.TrimSpace(diagnostic), "bwrap:") || strings.HasPrefix(diagnostic, "prlimit:") {
			return nil, "", fmt.Errorf("插件隔离进程无法启动：%s", clean(diagnostic))
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			if errors.Is(err, errResponseLimit) {
				return nil, "插件报告超出限制", nil
			}
			return nil, "", err
		}
		if exit.ExitCode() > 128 || exit.ExitCode() < 0 {
			return nil, fmt.Sprintf("插件扫描进程异常退出（%s）", exit.ProcessState), nil
		}
		return nil, "插件扫描进程未返回有效结果", nil
	}
	classes, problem, err := parseResponse(data)
	if err != nil {
		return nil, "插件返回了无效或不受支持的扫描结果", nil
	}
	return classes, problem, nil
}

func (s scanner) invoke(ctx context.Context, module Module) ([]byte, string, error) {
	args := []string{
		"--core=0", "--as=4294967296", "--fsize=8388608", "--", s.bwrap,
		"--unshare-all", "--die-with-parent", "--new-session", "--clearenv",
		"--setenv", "HOME", "/tmp", "--setenv", "PATH", "/usr/bin",
		"--ro-bind", "/usr", "/usr", "--ro-bind", s.helper, "/scanner",
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--chdir", "/tmp",
	}
	for _, path := range []string{"/bin", "/lib", "/lib64"} {
		if link, err := os.Readlink(path); err == nil {
			args = append(args, "--symlink", link, path)
		} else if info, err := os.Stat(path); err == nil && info.IsDir() {
			args = append(args, "--ro-bind", path, path)
		}
	}
	if module.Path == "" {
		args = append(args, "/scanner", "--protocol")
	} else {
		target := "/module/" + filepath.Base(module.Path)
		args = append(args, "--ro-bind", module.Path, target, "/scanner", module.Format, target)
	}
	cmd := exec.CommandContext(ctx, s.limit, args...)
	cmd.WaitDelay = time.Second
	out, errOut := boundedBuffer{limit: maxResponse}, boundedBuffer{limit: 4096}
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return out.data.Bytes(), errOut.data.String(), err
}

var errResponseLimit = errors.New("plugin scan response exceeds limit")

type boundedBuffer struct {
	data  bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.data.Len() {
		return 0, errResponseLimit
	}
	return b.data.Write(p)
}

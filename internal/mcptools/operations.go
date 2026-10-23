package mcptools

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"icetudio/internal/config"
)

const maxWriteBytes = 256 * 1024

var fileMutationMu sync.Mutex

type WriteArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// nil means create only; non-nil is a compare-and-replace contract.
	ExpectedContent *string `json:"expectedContent,omitempty"`
}
type EditArgs struct {
	Path    string `json:"path"`
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}
type ExecArgs struct {
	Executable     string   `json:"executable"`
	Args           []string `json:"args"`
	Cwd            string   `json:"cwd"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
}

// Root each operation to an authorized directory. os.Root rejects symlink
// escapes even when an attacker changes a parent after the gateway check.
func openOperationParent(path string, unrestricted bool, roots []string) (*os.Root, string, error) {
	if !filepath.IsAbs(path) {
		return nil, "", errors.New("文件路径必须为绝对路径")
	}
	abs := filepath.Clean(path)
	if unrestricted {
		r, err := os.OpenRoot(filepath.Dir(abs))
		return r, filepath.Base(abs), err
	}
	for _, base := range roots {
		base, err := filepath.Abs(base)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(base, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		root, err := os.OpenRoot(base)
		if err != nil {
			continue
		}
		parent, err := root.OpenRoot(filepath.Dir(rel))
		root.Close()
		if err != nil {
			return nil, "", fmt.Errorf("路径不在授权工作区内：%w", err)
		}
		return parent, filepath.Base(rel), nil
	}
	return nil, "", errors.New("路径超出授权工作区")
}
func readMutationTarget(root *os.Root, name string) ([]byte, os.FileMode, bool, error) {
	f, err := root.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0600, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, errors.New("目标不是常规文件")
	}
	if info.Size() > maxWriteBytes {
		return nil, 0, false, errors.New("文件超过 256 KiB 编辑上限")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxWriteBytes+1))
	if err != nil {
		return nil, 0, false, err
	}
	if len(data) > maxWriteBytes || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, 0, false, errors.New("仅支持上限内的 UTF-8 文本文件")
	}
	return data, info.Mode().Perm(), true, nil
}
func WriteFile(ctx context.Context, args WriteArgs, unrestricted bool, roots []string) (string, error) {
	if len(args.Content) > maxWriteBytes || !utf8.ValidString(args.Content) || strings.ContainsRune(args.Content, 0) {
		return "", errors.New("写入内容必须是 256 KiB 内的 UTF-8 文本")
	}
	if args.ExpectedContent != nil && len(*args.ExpectedContent) > maxWriteBytes {
		return "", errors.New("预期内容过长")
	}
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	parent, name, err := openOperationParent(args.Path, unrestricted, roots)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	old, mode, exists, err := readMutationTarget(parent, name)
	if err != nil {
		return "", err
	}
	if exists && args.ExpectedContent == nil {
		return "", errors.New("文件已存在；覆盖必须提交 expectedContent")
	}
	if args.ExpectedContent != nil && (!exists || string(old) != *args.ExpectedContent) {
		return "", errors.New("文件内容已变化或不存在，拒绝覆盖")
	}
	return commitText(ctx, parent, name, args.Path, old, exists, mode, []byte(args.Content))
}
func EditFile(ctx context.Context, args EditArgs, unrestricted bool, roots []string) (string, error) {
	if args.OldText == "" || len(args.OldText) > maxWriteBytes || len(args.NewText) > maxWriteBytes {
		return "", errors.New("oldText 不能为空，编辑内容最多 256 KiB")
	}
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	parent, name, err := openOperationParent(args.Path, unrestricted, roots)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	old, mode, exists, err := readMutationTarget(parent, name)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", errors.New("文件不存在")
	}
	count := strings.Count(string(old), args.OldText)
	if count != 1 {
		return "", fmt.Errorf("oldText 必须精确匹配一处，实际匹配 %d 处", count)
	}
	content := strings.Replace(string(old), args.OldText, args.NewText, 1)
	if len(content) > maxWriteBytes || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return "", errors.New("编辑后内容不是上限内的 UTF-8 文本")
	}
	return commitText(ctx, parent, name, args.Path, old, true, mode, []byte(content))
}
func commitText(ctx context.Context, parent *os.Root, name, path string, old []byte, existed bool, mode os.FileMode, content []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	temp := ".icetudio-write-" + hex.EncodeToString(nonce[:])
	f, err := parent.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer parent.Remove(temp)
	if _, err = f.Write(content); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	current, _, exists, err := readMutationTarget(parent, name)
	if err != nil {
		return "", err
	}
	if exists != existed || !bytes.Equal(current, old) {
		return "", errors.New("提交前文件发生变化，拒绝覆盖")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Creation uses an exclusive destination to prevent overwriting a file
	// created by another process between the final check and commit.
	if !existed {
		if err := parent.Link(temp, name); err != nil {
			return "", err
		}
	} else if err := parent.Rename(temp, name); err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	result, _ := json.Marshal(map[string]any{"path": path, "bytes": len(content), "sha256": hex.EncodeToString(sum[:])})
	return string(result), nil
}

type cappedOutput struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := 6000 - len(b.data)
	if remaining > 0 {
		take := n
		if take > remaining {
			take = remaining
		}
		b.data = append(b.data, p[:take]...)
	}
	if n > remaining {
		b.truncated = true
	}
	return n, nil
}
func (b *cappedOutput) text() string { return strings.ToValidUTF8(string(b.data), "�") }
func ValidateExec(args ExecArgs) error {
	if args.Executable == "" || strings.ContainsRune(args.Executable, 0) || !filepath.IsAbs(args.Cwd) {
		return errors.New("exec 需要可执行文件名称与绝对工作目录")
	}
	if args.TimeoutSeconds < 0 || args.TimeoutSeconds > 600 || len(args.Args) > 128 {
		return errors.New("命令超时最多 600 秒，参数最多 128 项")
	}
	size := len(args.Executable)
	for _, a := range args.Args {
		size += len(a)
		if strings.ContainsRune(a, 0) {
			return errors.New("命令参数包含无效字符")
		}
	}
	if size > 64*1024 {
		return errors.New("命令参数过长")
	}
	return nil
}
func Exec(ctx context.Context, args ExecArgs) (string, error) {
	if err := ValidateExec(args); err != nil {
		return "", err
	}
	timeout := args.TimeoutSeconds
	if timeout == 0 {
		timeout = 120
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, args.Executable, args.Args...)
	cmd.Dir = args.Cwd
	paths, err := config.DefaultPaths()
	if err != nil {
		return "", err
	}
	tempDir := filepath.Join(paths.Data, "exec-tmp")
	if err := os.MkdirAll(tempDir, 0700); err != nil {
		return "", err
	}
	cmd.Env = append(BuildEnv(nil), "TMPDIR="+tempDir, "GOTMPDIR="+tempDir, "TMP="+tempDir, "TEMP="+tempDir)
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killExecutionTree(cmd) }
	cmd.WaitDelay = 500 * time.Millisecond
	stdout, stderr := &cappedOutput{}, &cappedOutput{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	waitErr := cmd.Wait()
	cleanupExecutionTree(cmd)
	if ctx.Err() != nil {
		return "", &UnknownOutcomeError{Err: ctx.Err()}
	}
	exitCode := 0
	if waitErr != nil {
		exitCode = -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
	}
	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
	detail := ""
	if waitErr != nil {
		detail = waitErr.Error()
	}
	out, errout := stdout.text(), stderr.text()
	truncated := stdout.truncated || stderr.truncated
	for {
		result, _ := json.Marshal(map[string]any{"error": detail, "exitCode": exitCode, "stdout": out, "stderr": errout, "truncated": truncated, "timedOut": timedOut, "passed": waitErr == nil && !timedOut})
		if len(result) <= 15000 {
			return string(result), nil
		}
		if len(out) >= len(errout) {
			out = strings.ToValidUTF8(out[:len(out)/2], "�")
		} else {
			errout = strings.ToValidUTF8(errout[:len(errout)/2], "�")
		}
		truncated = true
	}
}

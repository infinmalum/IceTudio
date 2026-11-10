//go:build linux

package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	wireSize   = 1245632
	headerSize = 256
	paramSize  = 64
	stateStart = headerSize + MaxParameters*paramSize
)

type Instance struct {
	mapping         []byte
	cmd             *exec.Cmd
	done            chan struct{}
	Latency         int
	Version, Vendor string
	Parameters      []Parameter
	State           []byte
}

// Shared is a C-accessible mmap. Its lifetime ends after the audio plan that
// references it is detached. The worker can never resize this mapping.
func (p *Instance) Shared() unsafe.Pointer { return unsafe.Pointer(&p.mapping[0]) }

func (p *Instance) Diagnostic() string {
	status := atomic.LoadUint32((*uint32)(unsafe.Pointer(&p.mapping[4])))
	if status == 4 {
		return "插件报告 I/O 或延迟变化"
	}
	if status != 2 {
		return ""
	}
	message, _, _ := bytes.Cut(p.mapping[28:headerSize], []byte{0})
	if len(message) == 0 {
		return ""
	}
	return clean(string(message))
}

func (p *Instance) NeedsRebuild() bool {
	return atomic.LoadUint32((*uint32)(unsafe.Pointer(&p.mapping[4]))) == 4
}

func (p *Instance) Kill() {
	if p.cmd != nil {
		_ = p.cmd.Process.Kill()
	}
}
func (p *Instance) Terminate() {
	if p.cmd != nil {
		p.Kill()
		<-p.done
	}
}
func (p *Instance) Close() {
	p.Terminate()
	p.cmd = nil
	if p.mapping != nil {
		_ = unix.Munmap(p.mapping)
		p.mapping = nil
	}
}

func openInstance(ctx context.Context, module Module, class Class, state []byte, offline bool, overrides []Parameter) (_ *Instance, err error) {
	app, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return startInstance(ctx, filepath.Join(filepath.Dir(app), "icetudio-plugin-run"), module, class, state, offline, nil, overrides)
}

// Freeze the exact verified bytes before Bubblewrap mounts the executable.
func pinBinary(ctx context.Context, module Module) (_ *os.File, binaryPath string, err error) {
	path, err := moduleBinary(module.Path)
	if err != nil {
		return nil, "", err
	}
	source, err := openRegular(path)
	if err != nil {
		return nil, "", err
	}
	defer source.Close()
	fd, err := unix.MemfdCreate("icetudio-verified-module", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, "", err
	}
	pinned := os.NewFile(uintptr(fd), "icetudio-verified-module")
	defer func() {
		if err != nil {
			pinned.Close()
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(pinned, hash), io.LimitReader(contextReader{ctx, source}, maxBinary+1))
	if err != nil {
		return nil, "", err
	}
	if n == 0 || n > maxBinary || hex.EncodeToString(hash.Sum(nil)) != module.SHA256 {
		return nil, "", fmt.Errorf("插件二进制已变化，请重新扫描")
	}
	if _, err = unix.FcntlInt(pinned.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_SHRINK|unix.F_SEAL_GROW|unix.F_SEAL_WRITE|unix.F_SEAL_SEAL); err != nil {
		return nil, "", err
	}
	if _, err = pinned.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	if module.Format == "clap" {
		return pinned, "", nil
	}
	relative, err := filepath.Rel(module.Path, path)
	return pinned, relative, err
}

func startInstance(ctx context.Context, helper string, module Module, class Class, state []byte, offline bool, display *editorDisplay, overrides []Parameter) (_ *Instance, err error) {
	if len(state) > MaxState {
		return nil, fmt.Errorf("插件状态超出大小限制")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if st, err := os.Lstat(helper); err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
		return nil, fmt.Errorf("请安装与 IceTudio 同目录的 icetudio-plugin-run")
	}
	fd, err := unix.MemfdCreate("icetudio-plugin", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "icetudio-plugin")
	defer file.Close()
	if err = unix.Ftruncate(fd, wireSize); err != nil {
		return nil, err
	}
	if _, err = unix.FcntlInt(file.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_SHRINK|unix.F_SEAL_GROW|unix.F_SEAL_SEAL); err != nil {
		return nil, err
	}
	mem, err := unix.Mmap(fd, 0, wireSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	p := &Instance{mapping: mem, done: make(chan struct{}), Version: class.Version, Vendor: class.Vendor}
	defer func() {
		if err != nil {
			p.Close()
		}
	}()
	binary.NativeEndian.PutUint32(mem, 0x49434531)
	binary.NativeEndian.PutUint32(mem[24:], uint32(len(state)))
	copy(mem[stateStart:], state)
	if len(overrides) != 0 {
		// Offset assertions also live in runtime_wire.h. The editor uses
		// the first input slot for committed parameter overrides.
		const inputOffset = stateStart + MaxState
		const idsOffset = inputOffset + 20
		if len(overrides) > MaxParameters {
			return nil, fmt.Errorf("插件参数超出上限")
		}
		binary.NativeEndian.PutUint32(mem[inputOffset+12:], uint32(len(overrides)))
		for i, v := range overrides {
			if math.IsNaN(v.Value) || v.Value < 0 || v.Value > 1 {
				return nil, fmt.Errorf("插件参数无效")
			}
			binary.NativeEndian.PutUint32(mem[idsOffset+i*4:], v.ID)
			binary.NativeEndian.PutUint32(mem[idsOffset+(MaxParameters+i)*4:], math.Float32bits(float32(v.Value)))
		}
	}

	verified, relative, err := pinBinary(ctx, module)
	if err != nil {
		return nil, err
	}
	defer verified.Close()
	s := scanner{helper: helper, bwrap: "/usr/bin/bwrap", limit: "/usr/bin/prlimit"}
	target := "/module/" + filepath.Base(module.Path)
	mode := "live"
	if offline {
		mode = "offline"
	}
	// Bubblewrap copies the pinned module before the tighter worker limit
	// takes effect. The copy is bounded by pinBinary's size check.
	args := s.isolatedArgs("536870912")
	if display != nil {
		mode = "editor"
		args = append(args, "--setenv", "DISPLAY", display.name, "--setenv", "XAUTHORITY", "/editor-authority",
			"--ro-bind", display.auth, "/editor-authority", "--ro-bind", display.socket, display.socket)
	}
	binaryTarget := target
	if relative != "" {
		args = append(args, "--ro-bind", module.Path, target)
		binaryTarget = filepath.Join(target, relative)
	}
	args = append(args, "--perms", "0555", "--ro-bind-data", "3", binaryTarget, "/usr/bin/prlimit", "--fsize=8388608", "--", "/helper", module.Format, target, class.ID, class.Kind, mode)
	cmd := exec.Command(s.limit, args...)
	cmd.Stdin = file
	cmd.ExtraFiles = []*os.File{verified}
	diagnostic := boundedBuffer{limit: 4096}
	cmd.Stderr = &diagnostic
	cmd.WaitDelay = time.Second
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	p.cmd = cmd
	go func() {
		_ = cmd.Wait()
		atomic.StoreUint32((*uint32)(unsafe.Pointer(&mem[4])), 2)
		close(p.done)
	}()

	deadline := time.NewTimer(scanTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for {
		switch atomic.LoadUint32((*uint32)(unsafe.Pointer(&mem[4]))) {
		case 1:
			if err = p.metadata(); err != nil {
				return nil, err
			}
			return p, nil
		case 2:
			message, _, _ := bytes.Cut(mem[28:headerSize], []byte{0})
			if len(message) > 0 {
				return nil, fmt.Errorf("插件启动失败：%s", clean(string(message)))
			}
			select {
			case <-p.done:
				if diagnostic.data.Len() > 0 {
					return nil, fmt.Errorf("插件启动失败：%s", clean(diagnostic.data.String()))
				}
			default:
			}
			return nil, fmt.Errorf("插件隔离进程提前退出")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("插件启动超时")
		case <-tick.C:
		}
	}
}

func (p *Instance) metadata() error {
	p.Parameters = nil
	mem := p.mapping
	latency := binary.NativeEndian.Uint32(mem[16:])
	params := binary.NativeEndian.Uint32(mem[20:])
	state := binary.NativeEndian.Uint32(mem[24:])
	if latency > MaxLatency || params > MaxParameters || state > MaxState {
		return fmt.Errorf("插件参数、延迟或状态超出限制")
	}
	p.Latency = int(latency)
	p.State = bytes.Clone(mem[stateStart : stateStart+int(state)])
	ids := make(map[uint32]bool, params)
	for i := 0; i < int(params); i++ {
		record := mem[headerSize+i*paramSize : headerSize+(i+1)*paramSize]
		id := binary.NativeEndian.Uint32(record)
		value := float64(math.Float32frombits(binary.NativeEndian.Uint32(record[4:])))
		if ids[id] || math.IsNaN(value) || value < 0 || value > 1 {
			return fmt.Errorf("插件报告无效的参数")
		}
		name, _, _ := bytes.Cut(record[8:], []byte{0})
		ids[id] = true
		p.Parameters = append(p.Parameters, Parameter{ID: id, Name: strings.ToValidUTF8(string(name), ""), Value: value})
	}
	return nil
}

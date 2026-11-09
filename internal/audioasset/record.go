package audioasset

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"icetudio/internal/project"
)

type Take struct {
	ID, ProjectID, TrackID, Kind, Device string
	Revision, Tick                       int64
	Monitor                              bool
	LoopFrames                           int64 `json:",omitempty"`
}
type TakeStatus struct {
	Take
	Active bool
	Frames int64
	Peak   int
	Error  string
}

type Recorder struct {
	data    string
	mu      sync.Mutex
	current TakeStatus
	cancel  context.CancelFunc
	done    chan struct{}
}

func NewRecorder(data string) *Recorder   { return &Recorder{data: data} }
func (r *Recorder) dir() string           { return filepath.Join(r.data, "audio-takes") }
func (r *Recorder) Path(id string) string { return filepath.Join(r.dir(), id+".pcm") }
func (r *Recorder) Status() TakeStatus    { r.mu.Lock(); defer r.mu.Unlock(); return r.current }

func validTake(t Take) bool {
	return project.ValidID(t.ID) && project.ValidID(t.ProjectID) && project.ValidID(t.TrackID) &&
		t.Revision >= 0 && t.LoopFrames >= 0 && t.LoopFrames <= project.MaxAudioFrames && (t.LoopFrames == 0 || t.Kind == "audio") && t.Tick >= 0 && t.Tick <= project.MaxTick && (t.Kind == "audio" || t.Kind == "midi")
}

func (r *Recorder) Start(t Take) error {
	if r.data == "" {
		return fmt.Errorf("未配置录音数据目录")
	}
	t.ID = project.NewID("take")
	if t.Kind != "audio" || !validTake(t) || len(t.Device) > 240 || strings.ContainsRune(t.Device, 0) {
		return fmt.Errorf("录音目标或设备无效")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current.Active {
		return fmt.Errorf("已有录音进行中")
	}
	if err := os.MkdirAll(r.dir(), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(r.Path(t.ID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(t)
	if err = Publish(context.Background(), filepath.Join(r.dir(), t.ID+".json"), func(f *os.File) error { _, err := f.Write(data); return err }); err != nil {
		f.Close()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	args := []string{"--raw", "--rate", "48000", "--channels", "2", "--format", "s16", "--latency", "256"}
	if t.Device != "" {
		args = append(args, "--target", t.Device)
	}
	cmd := exec.CommandContext(ctx, "pw-record", append(args, "-")...)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 2 * time.Second
	log := &limitedLog{}
	cmd.Stderr = log
	pipe, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		cancel()
		f.Close()
		return fmt.Errorf("打开 PipeWire 输入：%w", err)
	}
	r.cancel = cancel
	r.done = make(chan struct{})
	r.current = TakeStatus{Take: t, Active: true}
	go r.record(ctx, cancel, cmd, pipe, f, log, t.Monitor, r.done)
	return nil
}

func (r *Recorder) record(ctx context.Context, cancel context.CancelFunc, cmd *exec.Cmd, pipe io.Reader, f *os.File, log *limitedLog, monitor bool, done chan struct{}) {
	var samples chan []byte
	if monitor {
		samples = r.monitor(ctx)
	}
	var frames int64
	var activity atomic.Int64
	var timedOut atomic.Bool
	activity.Store(time.Now().UnixNano())
	watchDone := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-watchDone:
				return
			case <-t.C:
				if time.Since(time.Unix(0, activity.Load())) > 5*time.Second {
					timedOut.Store(true)
					cancel()
					return
				}
			}
		}
	}()

	readErr := recordPCM(pipe, f, samples, func(count int64, peak int) {
		activity.Store(time.Now().UnixNano())
		frames = count
		r.mu.Lock()
		r.current.Frames, r.current.Peak = count, peak
		r.mu.Unlock()
	})
	close(watchDone)
	if samples != nil {
		close(samples)
	}
	if readErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	closeErr := f.Close()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current.Active = false
	r.current.Frames = frames
	r.current.Peak = 0
	if timedOut.Load() {
		r.current.Error = "音频输入超过 5 秒无采样，已保留 take"
	} else if readErr != nil {
		r.current.Error = readErr.Error()
	} else if closeErr != nil {
		r.current.Error = closeErr.Error()
	} else if ctx.Err() == nil {
		r.current.Error = "音频输入已停止"
		if waitErr != nil {
			r.current.Error = fmt.Sprintf("音频输入断开：%v %s", waitErr, log.String())
		}
	}
	cancel()
	close(done)
}

func recordPCM(in io.Reader, f *os.File, monitor chan []byte, progress func(int64, int)) error {
	buf := make([]byte, 4096)
	var frames, lastSync int64
	for {
		n, readErr := io.ReadFull(in, buf)
		n -= n % FrameBytes
		n = min(n, int((int64(project.MaxAudioFrames)-frames)*FrameBytes))
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			frames += int64(n / FrameBytes)
			if frames-lastSync >= project.AudioRate/2 {
				if err := f.Sync(); err != nil {
					return err
				}
				lastSync = frames
			}
			if monitor != nil {
				select {
				case monitor <- append([]byte(nil), buf[:n]...):
				default:
				}
			}
			peak := 0
			for i := 0; i < n; i += 2 {
				v := int(int16(binary.LittleEndian.Uint16(buf[i:])))
				peak = max(peak, max(v, -v))
			}
			progress(frames, peak)
		}
		if frames >= project.MaxAudioFrames {
			if err := f.Sync(); err != nil {
				return err
			}
			return fmt.Errorf("录音达到十分钟上限，已写入 take 保留")
		}
		if readErr != nil {
			if err := f.Sync(); err != nil {
				return err
			}
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
				return nil
			}
			return readErr
		}
	}
}

func (r *Recorder) monitor(ctx context.Context) chan []byte {
	ch := make(chan []byte, 4)
	reader, writer := io.Pipe()
	cmd := exec.CommandContext(ctx, "pw-play", "--raw", "--rate", "48000", "--channels", "2", "--format", "s16", "--latency", "256", "-")
	cmd.Stdin = reader
	go func() {
		defer reader.Close()
		if err := cmd.Run(); err != nil && ctx.Err() == nil {
			r.mu.Lock()
			r.current.Error = "监听输出不可用，录音继续"
			r.mu.Unlock()
		}
	}()
	go func() {
		defer writer.Close()
		for buf := range ch {
			if _, err := writer.Write(buf); err != nil {
				return
			}
		}
	}()
	return ch
}

func (r *Recorder) Stop() {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (r *Recorder) List() ([]TakeStatus, error) {
	if r.data == "" {
		return nil, nil
	}
	files, err := os.ReadDir(r.dir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	status := r.Status()
	var out []TakeStatus
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(f.Name(), ".json")
		t, err := r.read(id)
		if err != nil {
			continue
		}
		path := r.Path(id)
		if t.Kind == "midi" {
			path = r.MIDIPath(id)
		}
		fi, err := os.Stat(path)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if id == status.ID {
			out = append(out, status)
		} else {
			count := fi.Size() / FrameBytes
			if t.Kind == "midi" {
				count = -1
			}
			out = append(out, TakeStatus{Take: t, Frames: count})
		}
	}
	return out, nil
}
func (r *Recorder) read(id string) (Take, error) {
	if !project.ValidID(id) {
		return Take{}, fmt.Errorf("take 标识无效")
	}
	f, err := os.Open(filepath.Join(r.dir(), id+".json"))
	if err != nil {
		return Take{}, err
	}
	defer f.Close()
	var t Take
	err = json.NewDecoder(io.LimitReader(f, 4096)).Decode(&t)
	if err != nil || t.ID != id || !validTake(t) {
		return Take{}, fmt.Errorf("take 清单无效")
	}
	return t, nil
}

func (r *Recorder) Recover(id string) (Take, project.AudioAsset, error) {
	t, err := r.read(id)
	if err != nil {
		return t, project.AudioAsset{}, err
	}
	if t.Kind != "audio" {
		return t, project.AudioAsset{}, fmt.Errorf("只能恢复音频 take")
	}
	if status := r.Status(); status.Active && status.ID == id {
		return t, project.AudioAsset{}, fmt.Errorf("请先停止录音")
	}
	f, err := os.Open(r.Path(id))
	if err != nil {
		return t, project.AudioAsset{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return t, project.AudioAsset{}, err
	}
	stage, err := stagePCM(r.data, io.LimitReader(f, fi.Size()/FrameBytes*FrameBytes))
	if err != nil {
		return t, project.AudioAsset{}, err
	}
	defer os.Remove(stage)
	asset, err := publishStage(r.data, "录音 "+id, stage)
	asset.TakeID, asset.TakeLoopFrames = id, t.LoopFrames
	return t, asset, err
}

// ReadTakeWindow reads completed frames from an original recording without
// publishing a project asset. Preview callers bound the requested duration.
func (r *Recorder) ReadTakeWindow(id string, source, frames int64) ([]byte, error) {
	t, err := r.read(id)
	if err != nil {
		return nil, err
	}
	if t.Kind != "audio" || source < 0 || source >= project.MaxAudioFrames || frames < 1 || frames > 20*project.AudioRate {
		return nil, fmt.Errorf("录音预听窗口无效")
	}
	if status := r.Status(); status.Active && status.ID == id {
		return nil, fmt.Errorf("请先停止录音")
	}
	f, err := openAudioSource(r.Path(id))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || frames > fi.Size()/FrameBytes-source {
		return nil, fmt.Errorf("录音预听超出已写入帧")
	}
	out := make([]byte, frames*FrameBytes)
	_, err = f.ReadAt(out, source*FrameBytes)
	return out, err
}

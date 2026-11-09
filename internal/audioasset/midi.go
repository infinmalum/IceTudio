package audioasset

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"icetudio/internal/project"
)

type Device struct{ ID, Name string }

type MIDIFrame struct {
	Nanos int64  `json:"nanos"`
	Data  []byte `json:"data"`
}

func (r *Recorder) MIDIPath(id string) string { return filepath.Join(r.dir(), id+".midi-events") }

func (r *Recorder) StartMIDI(t Take) error {
	if r.data == "" {
		return fmt.Errorf("未配置录音数据目录")
	}
	t.ID = project.NewID("take")
	if t.Kind != "midi" || !validTake(t) {
		return fmt.Errorf("MIDI take 目标无效")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current.Active {
		return fmt.Errorf("已有录音进行中")
	}
	if err := os.MkdirAll(r.dir(), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(r.MIDIPath(t.ID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(t)
	if err = Publish(context.Background(), filepath.Join(r.dir(), t.ID+".json"), func(f *os.File) error { _, err := f.Write(data); return err }); err != nil {
		f.Close()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan MIDIFrame, 512)
	failures := make(chan error, 1)
	fail := func(err error) {
		select {
		case failures <- err:
		default:
		}
	}
	start := time.Now()
	stop, err := listenMIDI(t.Device, func(data []byte) {
		if len(data) < 2 || len(data) > 3 || data[0] < 0x80 || data[0] >= 0xf0 {
			return
		}
		entry := MIDIFrame{Nanos: time.Since(start).Nanoseconds(), Data: append([]byte(nil), data...)}
		select {
		case ch <- entry:
		default:
			fail(fmt.Errorf("MIDI 输入过载，保留已写事件"))
		}
	}, fail)
	if err != nil {
		cancel()
		f.Close()
		return err
	}
	r.cancel, r.done = cancel, make(chan struct{})
	r.current = TakeStatus{Take: t, Active: true}
	go r.writeMIDI(ctx, cancel, stop, f, ch, failures, r.done)
	return nil
}

func (r *Recorder) writeMIDI(ctx context.Context, cancel context.CancelFunc, stop func(), f *os.File, ch chan MIDIFrame, failures chan error, done chan struct{}) {
	timeout := time.NewTimer(10 * time.Minute)
	defer timeout.Stop()
	syncEvery := time.NewTicker(500 * time.Millisecond)
	defer syncEvery.Stop()
	encoder := json.NewEncoder(f)
	count := int64(0)
	write := func(e MIDIFrame) error {
		if count >= project.MaxNotes {
			return fmt.Errorf("MIDI 录音事件数达到上限")
		}
		if err := encoder.Encode(e); err != nil {
			return err
		}
		count++
		r.mu.Lock()
		r.current.Frames = count
		r.mu.Unlock()
		return nil
	}
	var failure error
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case failure = <-failures:
			break loop
		case <-timeout.C:
			failure = fmt.Errorf("MIDI 录音达到十分钟上限")
			break loop
		case <-syncEvery.C:
			if failure = f.Sync(); failure != nil {
				break loop
			}
		case e := <-ch:
			if failure = write(e); failure != nil {
				break loop
			}
		}
	}
	stop()
	if failure == nil {
	drain:
		for {
			select {
			case e := <-ch:
				if failure = write(e); failure != nil {
					break drain
				}
			default:
				break drain
			}
		}
	}
	if err := f.Sync(); failure == nil {
		failure = err
	}
	if err := f.Close(); failure == nil {
		failure = err
	}
	r.mu.Lock()
	r.current.Active = false
	if failure != nil {
		r.current.Error = failure.Error()
	}
	r.mu.Unlock()
	cancel()
	close(done)
}

func (r *Recorder) RecoverMIDI(id string) (Take, []MIDIFrame, error) {
	t, err := r.read(id)
	if err != nil {
		return t, nil, err
	}
	if t.Kind != "midi" {
		return t, nil, fmt.Errorf("不是 MIDI take")
	}
	if status := r.Status(); status.Active && status.ID == id {
		return t, nil, fmt.Errorf("请先停止录音")
	}
	f, err := os.Open(r.MIDIPath(id))
	if err != nil {
		return t, nil, err
	}
	defer f.Close()
	rows := bufio.NewScanner(f)
	rows.Buffer(make([]byte, 128), 512)
	var out []MIDIFrame
	for rows.Scan() {
		var e MIDIFrame
		if json.Unmarshal(rows.Bytes(), &e) != nil {
			break
		}
		if e.Nanos < 0 || e.Nanos > int64(10*time.Minute) || len(e.Data) < 2 || len(e.Data) > 3 || len(out) >= project.MaxNotes {
			return t, nil, fmt.Errorf("MIDI take 事件越界")
		}
		out = append(out, e)
	}
	if err = rows.Err(); err != nil {
		return t, nil, err
	}
	if len(out) == 0 {
		return t, nil, fmt.Errorf("没有可恢复的 MIDI 事件")
	}
	return t, out, nil
}

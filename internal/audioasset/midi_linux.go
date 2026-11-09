//go:build linux && cgo

package audioasset

import (
	"fmt"
	"strconv"

	"gitlab.com/gomidi/midi/v2/drivers"
	"gitlab.com/gomidi/midi/v2/drivers/rtmididrv"
)

func MIDIDevices() ([]Device, error) {
	d, err := rtmididrv.New()
	if err != nil {
		return nil, err
	}
	defer d.Close()
	ports, err := d.Ins()
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(ports))
	for _, p := range ports {
		out = append(out, Device{ID: strconv.Itoa(p.Number()), Name: p.String()})
	}
	return out, nil
}

func listenMIDI(device string, receive func([]byte), failed func(error)) (func(), error) {
	d, err := rtmididrv.New()
	if err != nil {
		return nil, err
	}
	ports, err := d.Ins()
	if err != nil {
		d.Close()
		return nil, err
	}
	for _, p := range ports {
		if strconv.Itoa(p.Number()) != device {
			continue
		}
		if err := p.Open(); err != nil {
			d.Close()
			return nil, err
		}
		stop, err := p.Listen(func(data []byte, _ int32) { receive(data) }, drivers.ListenConfig{OnErr: failed})
		if err != nil {
			d.Close()
			return nil, err
		}
		return func() { stop(); d.Close() }, nil
	}
	d.Close()
	return nil, fmt.Errorf("MIDI 输入设备不存在")
}

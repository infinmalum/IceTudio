//go:build !linux || !cgo

package audioasset

import "fmt"

func MIDIDevices() ([]Device, error) { return nil, fmt.Errorf("当前构建没有 ALSA MIDI 输入") }
func listenMIDI(string, func([]byte), func(error)) (func(), error) {
	return nil, fmt.Errorf("当前构建没有 ALSA MIDI 输入")
}

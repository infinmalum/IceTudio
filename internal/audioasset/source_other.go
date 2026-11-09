//go:build !linux

package audioasset

import "os"

func openAudioSource(path string) (*os.File, error) { return os.Open(path) }

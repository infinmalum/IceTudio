package audioasset

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

func AudioDevices(ctx context.Context) ([]Device, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pw-dump")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(out, 8<<20+1))
	if len(data) > 8<<20 {
		cancel()
		_ = cmd.Wait()
		return nil, fmt.Errorf("PipeWire 设备清单过大")
	}
	wait := cmd.Wait()
	if err != nil {
		return nil, err
	}
	if wait != nil {
		return nil, wait
	}
	var items []struct {
		Info struct {
			Props map[string]any `json:"props"`
		} `json:"info"`
	}
	if err = json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	var devices []Device
	for _, node := range items {
		props := node.Info.Props
		class, _ := props["media.class"].(string)
		id, _ := props["node.name"].(string)
		name, _ := props["node.description"].(string)
		if id != "" && strings.HasPrefix(class, "Audio/Source") {
			if name == "" {
				name = id
			}
			devices = append(devices, Device{ID: id, Name: name})
		}
	}
	return devices, nil
}

package pluginhost

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const maxResponse = 180 << 10

func parseResponse(data []byte) ([]Class, string, error) {
	if len(data) > maxResponse || len(data) == 0 || data[len(data)-1] != '\n' {
		return nil, "", errors.New("invalid scan response length")
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) < 2 || len(lines) > 1+maxClasses || lines[0] != protocol {
		return nil, "", errors.New("invalid scan protocol")
	}
	classes := make([]Class, 0, len(lines)-1)
	for _, line := range lines[1:] {
		fields := strings.Split(line, "\t")
		kind := fields[0]
		if kind != "error" && kind != "plugin" || kind == "error" && (len(lines) != 2 || len(fields) != 2) ||
			kind == "plugin" && len(fields) != 6 {
			return nil, "", errors.New("invalid scan record")
		}
		for i := 1; i < len(fields); i++ {
			if len(fields[i]) > 512 {
				return nil, "", errors.New("scan field exceeds limit")
			}
			value, err := hex.DecodeString(fields[i])
			if err != nil {
				return nil, "", fmt.Errorf("invalid scan field: %w", err)
			}
			fields[i] = string(value)
		}
		if kind == "error" {
			return nil, clean(fields[1]), nil
		}
		classes = append(classes, Class{ID: fields[1], Name: fields[2], Vendor: fields[3], Version: fields[4], Kind: fields[5]})
	}
	if !validClasses(classes) {
		return nil, "", errors.New("invalid plugin classes")
	}
	return classes, "", nil
}

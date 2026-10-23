package cli

import (
	"path/filepath"
	"strings"
)

// logPath is the host's log file inside the data directory. The CLI reads it
// directly — same user, same machine, so no IPC round-trip is needed.
func logPath(dataDir string) string {
	return filepath.Join(dataDir, "icetudio.log")
}

func splitLines(data string) []string {
	if data == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(data, "\n"), "\n")
}

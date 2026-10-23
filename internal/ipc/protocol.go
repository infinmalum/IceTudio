// Package ipc connects CLI clients to the single running host of a data
// directory. The host owns the database, the run loops and the MCP
// processes; clients only talk to the same application service the desktop
// UI uses. Transport is newline-delimited JSON over a Unix socket locked to
// the data directory, guarded by a per-host token plus (on Linux) a
// SO_PEERCRED identity check.
package ipc

import (
	"encoding/json"

	"icetudio/internal/app"
)

// Protocol version of the handshake; a client built against a different
// protocol is refused early instead of failing on unknown methods.
const ProtocolVersion = 1

// Hello is the first line a client sends after connecting.
type Hello struct {
	Hello   string `json:"hello"`   // always "icetudio"
	Token   string `json:"token"`   // host token from the data directory
	Version int    `json:"version"` // ProtocolVersion
}

// Welcome is the host's reply to a valid hello. A rejected hello closes the
// connection after a single error line.
type Welcome struct {
	Welcome string `json:"welcome"` // always "icetudio"
	Version int    `json:"version"`
}

// Request is one method call from the client.
type Request struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is the reply to one request. Result is only set when Error is
// empty.
type Response struct {
	ID     int64           `json:"id"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// EventLine wraps a post-commit service event pushed to a client that has
// not disabled event delivery. Events may repeat; consumers deduplicate by
// run status and sequence, exactly like the desktop event feed.
type EventLine struct {
	Event app.Event `json:"event"`
}

// Wire file names inside the data directory.
const (
	SocketName = "ipc.sock"
	TokenName  = "host.token"
)

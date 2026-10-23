package ipc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"icetudio/internal/app"
	"icetudio/internal/materials"
	"icetudio/internal/storage"
)

// Server owns the host side of the IPC: one Unix socket per data directory,
// token-based handshake with a same-UID peer check, request dispatch to the
// application service and event broadcast. Clients never touch the database
// or MCP processes directly, and a disconnect never cancels a run.
type Server struct {
	svc        *app.Service
	socketPath string
	tokenPath  string
	token      string

	mu       sync.Mutex
	closed   bool
	conns    map[*connBox]bool
	listener net.Listener
}

type connBox struct {
	conn   net.Conn
	write  sync.Mutex
	events chan app.Event
	stop   func()
	done   chan struct{}
}

// Start prepares the socket and token inside dataDir and begins accepting.
// The host lock already guarantees a single host per data directory, so a
// stale socket file from a crashed host can be removed safely.
func Start(svc *app.Service, dataDir string) (*Server, error) {
	if svc == nil {
		return nil, errors.New("应用服务未就绪")
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("生成宿主令牌: %w", err)
	}
	server := &Server{
		svc:        svc,
		socketPath: filepath.Join(dataDir, SocketName),
		tokenPath:  filepath.Join(dataDir, TokenName),
		token:      hex.EncodeToString(tokenBytes),
		conns:      map[*connBox]bool{},
	}
	if err := os.WriteFile(server.tokenPath, []byte(server.token+"\n"), 0600); err != nil {
		return nil, fmt.Errorf("写入宿主令牌: %w", err)
	}
	os.Remove(server.socketPath)
	listener, err := net.Listen("unix", server.socketPath)
	if err != nil {
		return nil, fmt.Errorf("监听 IPC 套接字: %w", err)
	}
	if err := os.Chmod(server.socketPath, 0600); err != nil {
		listener.Close()
		return nil, fmt.Errorf("收紧 IPC 套接字权限: %w", err)
	}
	server.listener = listener
	go server.acceptLoop()
	return server, nil
}

// Close stops accepting, disconnects clients and removes the socket and the
// token file.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	listener := s.listener
	boxes := make([]*connBox, 0, len(s.conns))
	for box := range s.conns {
		boxes = append(boxes, box)
	}
	s.conns = map[*connBox]bool{}
	s.mu.Unlock()

	var err error
	if listener != nil {
		err = listener.Close()
	}
	for _, box := range boxes {
		box.conn.Close()
		box.stop()
		<-box.done
	}
	os.Remove(s.socketPath)
	os.Remove(s.tokenPath)
	return err
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReaderSize(conn, 1<<20)
	var hello Hello
	line, err := readLine(reader)
	if err != nil {
		conn.Close()
		return
	}
	if err := json.Unmarshal(line, &hello); err != nil || hello.Hello != "icetudio" || hello.Version != ProtocolVersion || hello.Token != s.token || !samePeer(conn) {
		writeLine(conn, Response{Error: "IPC 身份校验失败"})
		conn.Close()
		return
	}
	conn.SetDeadline(time.Time{})
	if err := writeLine(conn, Welcome{Welcome: "icetudio", Version: ProtocolVersion}); err != nil {
		conn.Close()
		return
	}

	box := &connBox{conn: conn, events: make(chan app.Event, 256), done: make(chan struct{})}
	box.stop = s.svc.Subscribe(func(evt app.Event) {
		select {
		case box.events <- evt:
		default:
			// The client reconciles from persisted state; a dropped event
			// only costs immediacy, never correctness.
		}
	})
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		box.stop()
		conn.Close()
		return
	}
	s.conns[box] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, box)
		s.mu.Unlock()
		box.stop()
		close(box.done)
		conn.Close()
	}()
	go box.pump()

	for {
		line, err := readLine(reader)
		if err != nil {
			return
		}
		var request Request
		if err := json.Unmarshal(line, &request); err != nil {
			_ = box.writeResponse(Response{Error: "请求格式无效"})
			continue
		}
		result, handlerErr := s.dispatch(context.Background(), request)
		response := Response{ID: request.ID}
		if handlerErr != nil {
			response.Error = handlerErr.Error()
		} else if result != nil {
			encoded, err := json.Marshal(result)
			if err != nil {
				response.Error = fmt.Sprintf("编码结果失败: %v", err)
			} else {
				response.Result = encoded
			}
		}
		if err := box.writeResponse(response); err != nil {
			return
		}
	}
}

func (b *connBox) pump() {
	for evt := range b.events {
		encoded, err := json.Marshal(EventLine{Event: evt})
		if err != nil {
			continue
		}
		if !b.writeLine(encoded) {
			return
		}
	}
}

func (b *connBox) writeResponse(response Response) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if !b.writeLine(encoded) {
		return io.EOF
	}
	return nil
}

func (b *connBox) writeLine(payload []byte) bool {
	b.write.Lock()
	defer b.write.Unlock()
	b.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := b.conn.Write(append(payload, '\n')); err != nil {
		return false
	}
	return true
}

func readLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	return line[:len(line)-1], nil
}

func writeLine(conn net.Conn, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	_, err = conn.Write(encoded)
	return err
}

// dispatch routes one request to the application service. Every method maps
// to the exact call the desktop UI makes, so ownership checks, approval
// rules and exfiltration gates behave identically over IPC.
func (s *Server) dispatch(ctx context.Context, request Request) (any, error) {
	switch request.Method {
	case "skillDrafts.start", "skillDrafts.list", "skillDrafts.edit", "skillDrafts.apply", "skillDrafts.discard", "skillDrafts.withdraw":
		return s.dispatchSkillDraft(request)
	case "conversations.options", "conversations.searchAvailability", "conversations.searchRecords", "conversations.list", "conversations.get", "conversations.create", "conversations.start", "contexts.get", "contexts.update", "components.list":
		return s.dispatchConversation(request)
	case "projects.list", "projects.get", "projects.query", "projects.create", "projects.delete", "projects.edit", "projects.undo", "projects.redo", "projects.transaction", "projects.history", "projects.import", "projects.export", "projects.bind":
		return s.dispatchProjects(ctx, request)
	case "status":
		return s.svc.GetStatus(), nil

	case "configs.list":
		return s.svc.ListRuntimeConfigs()
	case "configs.get":
		var params struct {
			ID string `json:"id"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.GetRuntimeConfig(params.ID)
	case "configs.update":
		var params struct {
			Config storage.RuntimeConfig `json:"config"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.UpdateRuntimeConfig(params.Config)
	case "configs.restoreDefault":
		var params struct {
			Revision int `json:"revision"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.RestoreRuntimeConfigDefault(params.Revision)

	case "sessions.messages":
		var params struct {
			SessionID string `json:"sessionId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.ListMessages(params.SessionID)

	case "runs.list":
		var params struct {
			SessionID string `json:"sessionId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.ListRuns(params.SessionID)
	case "tasks.graph", "tasks.reviews", "tasks.retrospectives", "runs.get":
		var params struct {
			RunID string `json:"runId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		if request.Method == "tasks.retrospectives" {
			return s.svc.ListTaskRetrospectives(params.RunID)
		}
		if request.Method == "tasks.reviews" {
			return s.svc.ListTaskReviews(params.RunID)
		}
		if request.Method == "tasks.graph" {
			return s.svc.GetTaskGraph(params.RunID)
		}
		return s.svc.GetRun(params.RunID)
	case "runs.children":
		var params struct {
			RunID string `json:"runId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.ListChildRuns(params.RunID)

	case "tasks.review", "tasks.retrospect":
		var params struct {
			SessionID string `json:"sessionId"`
			RunID     string `json:"runId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		if request.Method == "tasks.retrospect" {
			return s.svc.StartConversationRetrospective(params.SessionID, params.RunID)
		}
		return s.svc.StartConversationReview(params.SessionID, params.RunID)
	case "tasks.continue":
		var params struct {
			RunID       string             `json:"runId"`
			TaskID      string             `json:"taskId"`
			Input       string             `json:"input"`
			Attachments []materials.Upload `json:"attachments"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.ContinueConversationTask(params.RunID, params.TaskID, params.Input, params.Attachments...)
	case "tasks.cancel":
		var params struct {
			RunID string `json:"runId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return nil, s.svc.CancelTask(params.RunID)
	case "stream.pending":
		var params struct {
			RunID string `json:"runId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.PendingStreamText(params.RunID)

	case "calls.list":
		var params struct {
			SessionID string `json:"sessionId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.ListToolCalls(params.SessionID)
	case "calls.approve":
		var params struct {
			CallID string `json:"callId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.ApproveToolCall(params.CallID)
	case "calls.deny":
		var params struct {
			CallID string `json:"callId"`
			Note   string `json:"note"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.DenyToolCall(params.CallID, params.Note)
	case "approvals.pending":
		var params struct {
			ContextID string `json:"contextId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.PendingApprovals(params.ContextID)
	case "calls.resolve":
		var params struct {
			CallID string `json:"callId"`
			State  string `json:"state"`
			Result string `json:"result"`
			Note   string `json:"note"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.ResolveToolCall(params.CallID, params.State, params.Result, params.Note)

	case "exfil.list":
		var params struct {
			ContextID string `json:"contextId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.ListExfilGrants(params.ContextID)
	case "exfil.grant":
		var params struct {
			ContextID   string   `json:"contextId"`
			Destination string   `json:"destination"`
			Scopes      []string `json:"scopes"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.GrantExfil(params.ContextID, params.Destination, params.Scopes)
	case "exfil.revoke":
		var params struct {
			ContextID   string `json:"contextId"`
			Destination string `json:"destination"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return nil, s.svc.RevokeExfil(params.ContextID, params.Destination)

	case "credentials.status":
		return s.svc.ListCredentialStatus()

	case "summaries.get":
		var params struct {
			SessionID string `json:"sessionId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.GetSessionSummary(params.SessionID)

	case "plans.get":
		var params struct {
			SessionID string `json:"sessionId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return s.svc.GetSessionPlan(params.SessionID)
	case "plans.clear":
		var params struct {
			SessionID string `json:"sessionId"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return nil, s.svc.ClearSessionPlan(params.SessionID)

	default:
		return nil, fmt.Errorf("未知 IPC 方法 %q", request.Method)
	}
}

func decodeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("IPC 参数必须是单个 JSON 对象")
	}
	return nil
}

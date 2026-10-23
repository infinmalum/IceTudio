package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"icetudio/internal/app"
)

// Client is the host-side counterpart used by the CLI. It multiplexes
// request/response pairs and (optionally) service events over one connection.
type Client struct {
	conn net.Conn

	writeMu sync.Mutex
	idMu    sync.Mutex
	nextID  int64

	pendingMu sync.Mutex
	pending   map[int64]chan Response

	eventsMu sync.Mutex
	eventFn  func(app.Event)

	closedOnce sync.Once
	closed     chan struct{}
}

// Connect dials the data directory's host socket and performs the token
// handshake. wait bounds how long dialing may be retried while the host is
// coming up; zero means a single attempt.
func Connect(dataDir string, wait time.Duration) (*Client, error) {
	socketPath := filepath.Join(dataDir, SocketName)
	deadline := time.Now().Add(wait)
	var conn net.Conn
	var err error
	for {
		conn, err = net.Dial("unix", socketPath)
		if err == nil || wait <= 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return nil, fmt.Errorf("连接 IceTudio 宿主失败（宿主未运行？可先运行 icetudio serve 或桌面端）: %w", err)
	}
	token, err := os.ReadFile(filepath.Join(dataDir, TokenName))
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("读取宿主令牌失败: %w", err)
	}
	hello, err := json.Marshal(Hello{Hello: "icetudio", Token: string(trimSpace(token)), Version: ProtocolVersion})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := conn.Write(append(hello, '\n')); err != nil {
		conn.Close()
		return nil, fmt.Errorf("发送 IPC 握手: %w", err)
	}
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReaderSize(conn, 1<<20)
	line, err := readLine(reader)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("读取 IPC 握手响应: %w", err)
	}
	var welcome Welcome
	if jsonErr := json.Unmarshal(line, &welcome); jsonErr != nil || welcome.Welcome != "icetudio" {
		conn.Close()
		return nil, errors.New("IPC 身份校验失败：宿主拒绝了本客户端")
	}
	conn.SetReadDeadline(time.Time{})

	client := &Client{
		conn:    conn,
		pending: map[int64]chan Response{},
		closed:  make(chan struct{}),
	}
	go client.readLoop(reader)
	return client, nil
}

func trimSpace(data []byte) []byte {
	start, end := 0, len(data)
	for start < end && (data[start] == ' ' || data[start] == '\n' || data[start] == '\r' || data[start] == '\t') {
		start++
	}
	for end > start && (data[end-1] == ' ' || data[end-1] == '\n' || data[end-1] == '\r' || data[end-1] == '\t') {
		end--
	}
	return data[start:end]
}

func (c *Client) readLoop(reader *bufio.Reader) {
	for {
		line, err := readLine(reader)
		if err != nil {
			c.failPending(errors.New("宿主连接已断开"))
			return
		}
		// Distinguish pushes from responses by shape: events carry "event".
		var probe struct {
			Event *json.RawMessage `json:"event"`
		}
		if err := json.Unmarshal(line, &probe); err == nil && probe.Event != nil {
			var lineData EventLine
			if err := json.Unmarshal(line, &lineData); err == nil {
				c.eventsMu.Lock()
				fn := c.eventFn
				c.eventsMu.Unlock()
				if fn != nil {
					fn(lineData.Event)
				}
			}
			continue
		}
		var response Response
		if err := json.Unmarshal(line, &response); err != nil {
			continue
		}
		c.pendingMu.Lock()
		waiter, ok := c.pending[response.ID]
		if ok {
			delete(c.pending, response.ID)
		}
		c.pendingMu.Unlock()
		if ok {
			waiter <- response
			close(waiter)
		}
	}
}

func (c *Client) failPending(reason error) {
	c.closedOnce.Do(func() { close(c.closed) })
	c.pendingMu.Lock()
	waiters := make([]chan Response, 0, len(c.pending))
	for id, waiter := range c.pending {
		waiters = append(waiters, waiter)
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
	for _, waiter := range waiters {
		waiter <- Response{Error: reason.Error()}
		close(waiter)
	}
}

// OnEvent registers the event callback; pass nil to stop delivery.
func (c *Client) OnEvent(fn func(app.Event)) {
	c.eventsMu.Lock()
	c.eventFn = fn
	c.eventsMu.Unlock()
}

// Closed reports when the connection to the host is gone.
func (c *Client) Closed() <-chan struct{} { return c.closed }

// Call performs one request and decodes a successful result into result
// (which may be nil for unit-typed methods). Errors from the application
// service are returned verbatim so the CLI shows the exact reason.
func (c *Client) Call(method string, params, result any) error {
	var raw json.RawMessage
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = encoded
	}
	c.idMu.Lock()
	c.nextID++
	id := c.nextID
	c.idMu.Unlock()
	request, err := json.Marshal(Request{ID: id, Method: method, Params: raw})
	if err != nil {
		return err
	}
	waiter := make(chan Response, 1)
	c.pendingMu.Lock()
	c.pending[id] = waiter
	c.pendingMu.Unlock()

	c.writeMu.Lock()
	_, err = c.conn.Write(append(request, '\n'))
	c.writeMu.Unlock()
	if err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return fmt.Errorf("发送 IPC 请求失败: %w", err)
	}

	select {
	case response := <-waiter:
		if response.Error != "" {
			return errors.New(response.Error)
		}
		if result != nil && len(response.Result) > 0 {
			return json.Unmarshal(response.Result, result)
		}
		return nil
	case <-c.closed:
		return errors.New("宿主连接已断开")
	}
}

// Close disconnects from the host. Running tasks are unaffected: the host,
// not the client, owns them.
func (c *Client) Close() error {
	c.closedOnce.Do(func() { close(c.closed) })
	return c.conn.Close()
}

package aqf

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/console"
)

// Requests and responses are newline-delimited JSON on a filesystem-protected
// local socket. One connection may carry many requests, e.g. a shell session.
type Request struct {
	Method   string `json:"method"`
	DeviceID string `json:"device_id,omitempty"`
	Command  string `json:"command,omitempty"`
}

type Response struct {
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

type ServerInfo struct {
	Version  string `json:"version"`
	PID      int    `json:"pid"`
	DeviceID string `json:"device_id"`
}

const deviceBudget = 90 * time.Second // scan + connect + authenticate + work

func SocketPath() (string, error) {
	dir, err := console.Directory()
	return filepath.Join(dir, "aqf", "aqf.sock"), err
}

// Listen refuses to replace a live server and never opens a TCP listener.
func Listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("aqf endpoint is not a socket")
		}
		if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
			conn.Close()
			return nil, errors.New("aqf server is already running")
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

type Server struct {
	Link    *Link
	Version string
	Stop    func()
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	go func() { <-ctx.Done(); listener.Close() }()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.session(ctx, conn)
	}
}

func (s *Server) session(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 4096)
	encoder := json.NewEncoder(conn)
	for scanner.Scan() {
		var request Request
		response := Response{}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			response.Error = "invalid aqf request"
		} else {
			response = s.Handle(ctx, request)
		}
		if encoder.Encode(response) != nil {
			return
		}
		if request.Method == "kill" && s.Stop != nil {
			s.Stop()
		}
	}
}

func (s *Server) Handle(ctx context.Context, r Request) Response {
	if r.DeviceID != "" {
		if id, err := ble.NormalizeID(r.DeviceID); err != nil || id != s.Link.id {
			return Response{Error: "aqf server is bound to device " + s.Link.id + "; run aqf kill-server to select another"}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, deviceBudget)
	defer cancel()
	var result any
	var err error
	switch r.Method {
	case "version":
		result = ServerInfo{Version: s.Version, PID: os.Getpid(), DeviceID: s.Link.id}
	case "kill":
		result = true
	case "devices":
		result = []LinkState{s.Link.State()}
	case "disconnect":
		s.Link.Disconnect()
		result = s.Link.State()
	case "connect", "info":
		var info ble.DeviceInfo
		err = s.Link.Do(ctx, func(context.Context, Device) error { return nil })
		if state := s.Link.State(); state.Info != nil {
			info = *state.Info
		}
		if r.Method == "info" {
			result = info
		} else {
			result = s.Link.State()
		}
	case "status":
		var status json.RawMessage
		err = s.Link.Do(ctx, func(ctx context.Context, d Device) error {
			var e error
			status, e = d.Status(ctx)
			return e
		})
		result = status
	case "shell":
		if err = ble.ValidateShellCommand(r.Command); err != nil {
			break
		}
		var shell ble.ShellResult
		err = s.Link.Do(ctx, func(ctx context.Context, d Device) error {
			var e error
			shell, e = d.Shell(ctx, r.Command)
			return e
		})
		result = shell
	default:
		err = errors.New("unsupported aqf method")
	}
	if err != nil {
		return Response{Error: err.Error()}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return Response{Error: err.Error()}
	}
	return Response{Result: raw}
}

// Conn is one client connection to the server.
type Conn struct {
	conn    net.Conn
	scanner *bufio.Scanner
}

func Dial(ctx context.Context, path string) (*Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	return &Conn{conn: conn, scanner: scanner}, nil
}

func (c *Conn) Close() error { return c.conn.Close() }

// Call waits up to the server-side device budget plus a margin.
func (c *Conn) Call(r Request, result any) error {
	_ = c.conn.SetDeadline(time.Now().Add(deviceBudget + 10*time.Second))
	if err := json.NewEncoder(c.conn).Encode(r); err != nil {
		return err
	}
	if !c.scanner.Scan() {
		if err := c.scanner.Err(); err != nil {
			return err
		}
		return errors.New("aqf server closed the connection")
	}
	var response Response
	if err := json.Unmarshal(c.scanner.Bytes(), &response); err != nil {
		return err
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	if result == nil || len(response.Result) == 0 {
		return nil
	}
	return json.Unmarshal(response.Result, result)
}

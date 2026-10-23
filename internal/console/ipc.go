package console

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type Request struct {
	Action  string  `json:"action"`
	App     string  `json:"app,omitempty"`
	Path    string  `json:"path,omitempty"`
	Digest  string  `json:"digest,omitempty"`
	Slot    *int    `json:"slot,omitempty"`
	Version *uint32 `json:"version,omitempty"`
}
type PackageInfo struct {
	App     string `json:"app"`
	Version uint32 `json:"version"`
	ABI     uint32 `json:"abi"`
	Bytes   int    `json:"bytes"`
	Digest  string `json:"digest"`
}
type Response struct {
	State   *Snapshot    `json:"state,omitempty"`
	Error   string       `json:"error,omitempty"`
	Package *PackageInfo `json:"package,omitempty"`
}

func Directory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "ai-quota-frame"), nil
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "ai-quota-frame"), nil
}
func SocketPath() (string, error) {
	dir, err := Directory()
	return filepath.Join(dir, "console", "bridge.sock"), err
}

// Serve uses a filesystem-protected local socket, never a TCP listener.
func Serve(ctx context.Context, path string, handle func(Request) Response) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return errors.New("console endpoint is not a socket")
		}
		conn, dialErr := net.DialTimeout("unix", path, time.Second)
		if dialErr == nil {
			conn.Close()
			return errors.New("console is already running")
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(path)
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	go func() { <-ctx.Done(); listener.Close() }()
	// Short local requests are serialized; device work is enqueued separately.
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		var request Request
		if json.NewDecoder(io.LimitReader(conn, 4096)).Decode(&request) == nil {
			_ = json.NewEncoder(conn).Encode(handle(request))
		}
		conn.Close()
	}
}
func Call(ctx context.Context, path string, request Request) (Response, error) {
	var response Response
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return response, errors.New("后台服务未连接，请先启动 PhotoPainter Renderer")
	}
	defer conn.Close()
	deadline := time.Now().Add(4 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	if err = json.NewEncoder(conn).Encode(request); err != nil {
		return response, err
	}
	err = json.NewDecoder(io.LimitReader(conn, 256*1024)).Decode(&response)
	return response, err
}

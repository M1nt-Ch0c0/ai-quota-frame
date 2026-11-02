// Command aqf talks to a PhotoPainter through a background server that keeps
// the authenticated BLE session open, in the way adb talks through its server.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/aqf"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
)

var version = "dev"

const usage = `usage: aqf [-s DEVICE_ID] <command> [arguments]

Server:
  start-server   start the background server if it is not running
  kill-server    stop the background server and release the BLE link
  server         run the server in the foreground [--idle 10m] [--keepalive 20s]

Device:
  devices        show the server's device and connection state
  connect        open the BLE session now
  disconnect     close the BLE session; the next command reconnects
  info           authenticated discovery record
  status         host status JSON
  shell [CMD]    run CMD on the frame, or start an interactive shell

The device is -s, PHOTOFRAME_DEVICE_ID, or the saved pairing binding. Device
commands start the server automatically on first use.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("aqf", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(stderr, usage) }
	serial := flags.String("s", "", "device ID (12 hexadecimal digits)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	args = flags.Args()
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	command, args := args[0], args[1:]
	switch command {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "server":
		return runServer(*serial, args, stderr)
	case "start-server":
		if _, err := ensureServer(*serial, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "kill-server":
		conn, err := dialServer()
		if err != nil {
			return 0
		}
		defer conn.Close()
		if err = conn.Call(aqf.Request{Method: "kill"}, nil); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "version":
		fmt.Fprintf(stdout, "aqf client %s\n", version)
		if conn, err := dialServer(); err == nil {
			defer conn.Close()
			var info aqf.ServerInfo
			if conn.Call(aqf.Request{Method: "version"}, &info) == nil {
				fmt.Fprintf(stdout, "aqf server %s (pid %d, device %s)\n", info.Version, info.PID, info.DeviceID)
			}
		}
		return 0
	case "devices", "connect", "disconnect", "info", "status", "shell":
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", command, usage)
		return 2
	}
	conn, err := ensureServer(*serial, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer conn.Close()
	request := aqf.Request{Method: command, DeviceID: *serial}
	if command == "shell" {
		if len(args) == 0 {
			return interactiveShell(conn, request, stdin, stdout, stderr)
		}
		request.Command = strings.Join(args, " ")
		code, err := shellOnce(conn, request, stdout, stderr)
		if err != nil {
			fmt.Fprintln(stderr, "aqf:", err)
			return 1
		}
		return code
	}
	if len(args) != 0 {
		fmt.Fprintf(stderr, "%s takes no arguments\n", command)
		return 2
	}
	var result json.RawMessage
	if err = conn.Call(request, &result); err != nil {
		fmt.Fprintln(stderr, "aqf:", err)
		return 1
	}
	if command == "devices" {
		return printDevices(result, stdout)
	}
	fmt.Fprintln(stdout, string(result))
	return 0
}

func printDevices(raw json.RawMessage, stdout io.Writer) int {
	var states []aqf.LinkState
	if json.Unmarshal(raw, &states) != nil {
		return 1
	}
	fmt.Fprintln(stdout, "List of devices attached")
	for _, s := range states {
		state := "offline"
		if s.Connected {
			state = "device"
		}
		line := s.DeviceID + "\t" + state
		if s.LastError != "" && !s.Connected {
			line += "\t(" + s.LastError + ")"
		}
		fmt.Fprintln(stdout, line)
	}
	return 0
}

func shellOnce(conn *aqf.Conn, request aqf.Request, stdout, stderr io.Writer) (int, error) {
	var result ble.ShellResult
	if err := conn.Call(request, &result); err != nil {
		return 0, err
	}
	stdout.Write(result.Output)
	if n := len(result.Output); n > 0 && result.Output[n-1] != '\n' {
		fmt.Fprintln(stdout)
	}
	if result.Truncated {
		fmt.Fprintln(stderr, "aqf: output truncated by the device")
	}
	return int(result.Exit), nil
}

func interactiveShell(conn *aqf.Conn, request aqf.Request, stdin io.Reader, stdout, stderr io.Writer) int {
	var info aqf.ServerInfo
	if err := conn.Call(aqf.Request{Method: "version"}, &info); err != nil {
		fmt.Fprintln(stderr, "aqf:", err)
		return 1
	}
	// Connect before the first prompt so a missing device is reported at once.
	if err := conn.Call(aqf.Request{Method: "connect", DeviceID: request.DeviceID}, nil); err != nil {
		fmt.Fprintln(stderr, "aqf:", err)
		return 1
	}
	prompt := ""
	if f, ok := stdin.(*os.File); ok {
		if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			prompt = "photoframe:" + info.DeviceID + " $ "
		}
	}
	lines := bufio.NewScanner(stdin)
	code := 0
	for {
		fmt.Fprint(stdout, prompt)
		if !lines.Scan() {
			if prompt != "" {
				fmt.Fprintln(stdout)
			}
			return code
		}
		line := strings.TrimSpace(lines.Text())
		switch line {
		case "":
			continue
		case "exit", "quit", "logout":
			return code
		}
		request.Command = line
		var err error
		if code, err = shellOnce(conn, request, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "aqf:", err)
			code = 1
		}
	}
}

func dialServer() (*aqf.Conn, error) {
	path, err := aqf.SocketPath()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return aqf.Dial(ctx, path)
}

// ensureServer starts a detached "aqf server" like adb's fork-server.
func ensureServer(serial string, stderr io.Writer) (*aqf.Conn, error) {
	if conn, err := dialServer(); err == nil {
		return conn, nil
	}
	fmt.Fprintln(stderr, "* daemon not running; starting now *")
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	path, err := aqf.SocketPath()
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(filepath.Join(filepath.Dir(path), "server.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()
	args := []string{"server"}
	if serial != "" {
		args = []string{"-s", serial, "server"}
	}
	cmd := exec.Command(self, args...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = detached()
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(5 * time.Second)
	for {
		if conn, err := dialServer(); err == nil {
			fmt.Fprintln(stderr, "* daemon started successfully *")
			return conn, nil
		}
		select {
		case <-exited:
			return nil, errors.New("aqf server exited; see " + logFile.Name())
		case <-deadline:
			return nil, errors.New("aqf server did not start; see " + logFile.Name())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func runServer(serial string, args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("aqf server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	idle := flags.Duration("idle", 10*time.Minute, "close the BLE session after this much inactivity (0 keeps it open)")
	keepalive := flags.Duration("keepalive", 20*time.Second, "authenticated keepalive interval; the host closes idle sessions after 60s")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *keepalive <= 0 || *keepalive >= time.Minute {
		fmt.Fprintln(stderr, "--keepalive must be between 0 and 60s")
		return 2
	}
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	id := serial
	if id == "" {
		id = os.Getenv("PHOTOFRAME_DEVICE_ID")
	}
	if id == "" {
		bound, err := ble.LoadBinding()
		if err != nil {
			logger.Error("no device selected", "error", err)
			return 2
		}
		id = bound
	}
	if id == "" {
		logger.Error("no device selected; pass -s DEVICE_ID or pair a device first")
		return 2
	}
	id, err := ble.NormalizeID(id)
	if err != nil {
		logger.Error("invalid device ID", "error", err)
		return 2
	}
	token := os.Getenv("PHOTOFRAME_PUSH_TOKEN") // only for legacy v1 hosts
	link := aqf.NewLink(id, func(ctx context.Context, job func(context.Context, aqf.Device) error) error {
		return ble.WithClient(ctx, id, token, func(ctx context.Context, c *ble.Client) error { return job(ctx, c) })
	}, *keepalive, *idle)
	path, err := aqf.SocketPath()
	if err != nil {
		logger.Error("locate socket", "error", err)
		return 1
	}
	listener, err := aqf.Listen(path)
	if err != nil {
		logger.Error("listen", "error", err)
		return 1
	}
	defer os.Remove(path)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go link.Run(ctx)
	logger.Info("aqf server listening", "version", version, "device", id, "idle", idle.String())
	server := &aqf.Server{Link: link, Version: version, Stop: stop}
	if err := server.Serve(ctx, listener); err != nil {
		logger.Error("serve", "error", err)
		return 1
	}
	logger.Info("aqf server stopped")
	return 0
}

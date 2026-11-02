// Command aqf talks to a PhotoPainter through the ai-quota-frame daemon, which
// keeps the one authenticated BLE session open, as adb talks through its server.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/aqf"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
)

var version = "dev"

const usage = `usage: aqf [-s DEVICE_ID] <command> [arguments]

Server (the ai-quota-frame daemon in BLE mode):
  start-server   start the daemon if it is not running
  kill-server    stop the daemon, including automatic pushes

Device:
  devices        show the server's device and connection state
  connect        open the BLE session now
  disconnect     close the BLE session; the next command reconnects
  info           authenticated discovery record
  status         host status JSON
  shell [CMD]    run CMD on the frame, or start an interactive shell

The daemon serves one device (PHOTOFRAME_DEVICE_ID or the saved pairing); -s
only checks that it is the expected one. AQF_SOCKET selects the socket path.
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
	case "start-server":
		conn, err := startServer(stderr)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		conn.Close()
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
	// Starting the daemon also starts automatic pushes, so device commands
	// never start it implicitly.
	conn, err := dialServer()
	if err != nil {
		fmt.Fprintln(stderr, "aqf: daemon not running; start it with aqf start-server")
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

const launchLabel = "com.m1ntch0c0.ai-quota-frame"

// startServer starts the daemon: the installed macOS LaunchAgent when present,
// otherwise "ai-quota-frame daemon" next to aqf with this environment.
func startServer(stderr io.Writer) (*aqf.Conn, error) {
	if conn, err := dialServer(); err == nil {
		return conn, nil
	}
	fmt.Fprintln(stderr, "* daemon not running; starting now *")
	path, err := aqf.SocketPath()
	if err != nil {
		return nil, err
	}
	logName := filepath.Join(filepath.Dir(path), "daemon.log")
	var exited chan error
	if agent := launchAgent(); agent != "" {
		if out, err := exec.Command("launchctl", "kickstart", agent).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("launchctl kickstart failed: %s", strings.TrimSpace(string(out)))
		}
		logName = "the LaunchAgent log"
	} else {
		daemon, err := daemonExecutable()
		if err != nil {
			return nil, err
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		logFile, err := os.OpenFile(logName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return nil, err
		}
		defer logFile.Close()
		cmd := exec.Command(daemon, "daemon")
		cmd.Stdout, cmd.Stderr = logFile, logFile
		cmd.SysProcAttr = detached()
		if err = cmd.Start(); err != nil {
			return nil, err
		}
		exited = make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
	}
	// Chrome and data-source preflight run before the socket opens.
	deadline := time.After(20 * time.Second)
	for {
		if conn, err := dialServer(); err == nil {
			fmt.Fprintln(stderr, "* daemon started successfully *")
			return conn, nil
		}
		select {
		case <-exited:
			return nil, errors.New("daemon exited (check its configuration and PHOTOFRAME_PUSH_TRANSPORT=ble); see " + logName)
		case <-deadline:
			return nil, errors.New("daemon did not open the aqf socket; see " + logName)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func launchAgent() string {
	home, err := os.UserHomeDir()
	if err != nil || runtime.GOOS != "darwin" {
		return ""
	}
	if _, err = os.Stat(filepath.Join(home, "Library", "LaunchAgents", launchLabel+".plist")); err != nil {
		return ""
	}
	return fmt.Sprintf("gui/%d/%s", os.Getuid(), launchLabel)
}

func daemonExecutable() (string, error) {
	name := "ai-quota-frame"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if self, err := os.Executable(); err == nil {
		if candidate := filepath.Join(filepath.Dir(self), name); fileExists(candidate) {
			return candidate, nil
		}
	}
	return exec.LookPath(name)
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

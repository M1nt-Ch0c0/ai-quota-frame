package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
)

// No quota credentials, Chrome, listeners, or .env reads are needed for BLE CLI.
func runBLE(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: ai-quota-frame ble scan|info|status|install|activate|switch|rollback|remove|inspect [options]")
		return 2
	}
	command := args[0]
	flags := flag.NewFlagSet("ble "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	id := flags.String("device-id", os.Getenv("PHOTOFRAME_DEVICE_ID"), "stable device ID (12 hexadecimal digits)")
	app := flags.String("app", "photoframe", "application ID for activate/switch/rollback/remove")
	file := flags.String("file", "", "tools/module.py package file")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}
	var pkg []byte
	if command == "install" || command == "inspect" {
		f, err := os.Open(*file)
		if err != nil {
			fmt.Fprintln(stderr, "cannot open package file")
			return 2
		}
		pkg, err = io.ReadAll(io.LimitReader(f, ble.MaxPackageSize+1))
		f.Close()
		if err != nil {
			fmt.Fprintln(stderr, "cannot read package file")
			return 2
		}
		metadata, err := ble.InspectPackage(pkg)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		if command == "inspect" {
			if json.NewEncoder(stdout).Encode(metadata) != nil {
				return 1
			}
			return 0
		}
	}
	ops := map[string]byte{"activate": ble.OpActivate, "switch": ble.OpSwitch, "rollback": ble.OpRollback, "remove": ble.OpRemove}
	if command != "scan" && command != "info" && command != "status" && command != "install" && ops[command] == 0 {
		fmt.Fprintln(stderr, "unknown BLE command")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if command == "scan" {
		scanCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		devices, err := ble.Scan(scanCtx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if json.NewEncoder(stdout).Encode(devices) != nil {
			return 1
		}
		return 0
	}
	if _, err := ble.NormalizeID(*id); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	token := os.Getenv("PHOTOFRAME_PUSH_TOKEN")
	if token == "" {
		fmt.Fprintln(stderr, "PHOTOFRAME_PUSH_TOKEN is required in the process environment")
		return 2
	}
	for _, name := range []string{"CLIPROXY_MANAGEMENT_KEY", "CPAMP_ADMIN_KEY"} {
		if token == os.Getenv(name) {
			fmt.Fprintln(stderr, "device token must differ from management credentials")
			return 2
		}
	}
	budget := 11 * time.Minute
	if command == "install" {
		budget = 25 * time.Minute
	}
	operationCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	err := ble.WithClient(operationCtx, *id, token, func(ctx context.Context, c *ble.Client) error {
		switch command {
		case "info":
			return json.NewEncoder(stdout).Encode(c.Info())
		case "status":
			status, err := c.Status(ctx)
			if err != nil {
				return err
			}
			return json.NewEncoder(stdout).Encode(status)
		case "install":
			last := -1
			if err := c.Install(ctx, pkg, func(done, total int) {
				percent := done * 100 / total
				if percent != last {
					last = percent
					fmt.Fprintf(stderr, "upload %d%% (%d/%d bytes)\n", percent, done, total)
				}
			}); err != nil {
				return err
			}
			_, err := fmt.Fprintln(stdout, "staged; use ble activate explicitly to trial and confirm the application")
			return err
		default:
			if err := c.Manage(ctx, ops[command], *app); err != nil {
				return err
			}
			status, err := c.Status(ctx)
			if err != nil {
				return err
			}
			return json.NewEncoder(stdout).Encode(status)
		}
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintln(stderr, "BLE operation timed out; reconnect and query status before retrying activation")
		} else {
			fmt.Fprintln(stderr, err)
		}
		return 1
	}
	return 0
}

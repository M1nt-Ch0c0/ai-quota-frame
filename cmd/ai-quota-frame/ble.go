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
		fmt.Fprintln(stderr, "usage: ai-quota-frame ble scan|bind|unbind|info|status|push|install|activate|switch|rollback|remove|inspect|app-read|app-write [options]")
		return 2
	}
	command := args[0]
	flags := flag.NewFlagSet("ble "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	id := flags.String("device-id", os.Getenv("PHOTOFRAME_DEVICE_ID"), "stable device ID (12 hexadecimal digits)")
	app := flags.String("app", "photoframe", "application ID for activate/switch/rollback/remove")
	file := flags.String("file", "", "PNG for push, or tools/module.py package for installation")
	characteristic := flags.Uint("characteristic", 0, "zero-based application BLE characteristic index")
	text := flags.String("text", "", "UTF-8 value for app-write (at most 128 bytes)")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}
	if command == "unbind" {
		if err := ble.RemoveBinding(); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "device binding removed; restart the service to apply")
		return 0
	}
	if *id == "" && command != "bind" && command != "inspect" && command != "scan" {
		bound, err := ble.LoadBinding()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		*id = bound
	}
	var pkg []byte
	if command == "push" {
		f, err := os.Open(*file)
		if err != nil {
			fmt.Fprintln(stderr, "cannot open PNG file")
			return 2
		}
		pkg, err = io.ReadAll(io.LimitReader(f, ble.MaxImageSize+1))
		f.Close()
		if err != nil {
			fmt.Fprintln(stderr, "cannot read PNG file")
			return 2
		}
		if err = ble.ValidatePNG(pkg); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
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
	if command != "scan" && command != "bind" && command != "info" && command != "status" && command != "push" && command != "install" && command != "app-read" && command != "app-write" && ops[command] == 0 {
		fmt.Fprintln(stderr, "unknown BLE command")
		return 2
	}
	if *characteristic >= 4 || len(*text) > 128 {
		fmt.Fprintln(stderr, "application BLE index must be 0..3 and text at most 128 bytes")
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
	if command == "install" || command == "push" {
		budget = 90 * time.Minute
	}
	operationCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if command == "push" {
		sender, err := ble.NewImageSender(*id, token, *app)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		last := -1
		err = sender.Push(operationCtx, pkg, func(done, total int) {
			percent := done * 100 / total
			if percent != last {
				last = percent
				fmt.Fprintf(stderr, "image upload %d%% (%d/%d bytes)\n", percent, done, total)
			}
		})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "display completed; authenticated BLE receipt confirms refresh and final POWER_OFF")
		return 0
	}
	plans := make(chan managementPlan, 1)
	// At most the original read and one recovery read can finish, including a
	// native cleanup failure after the original read already succeeded.
	managementResult := make(chan json.RawMessage, 2)
	err := ble.WithClient(operationCtx, *id, token, func(ctx context.Context, c *ble.Client) error {
		switch command {
		case "bind":
			if err := ble.SaveBinding(c.Info().ID); err != nil {
				return err
			}
			_, err := fmt.Fprintln(stdout, "authenticated device ID saved separately from .env; restart the service to apply")
			return err
		case "app-write":
			if err := c.AppWrite(ctx, *app, uint32(*characteristic), []byte(*text)); err != nil {
				return err
			}
			_, err := fmt.Fprintln(stdout, "application write completed")
			return err
		case "app-read":
			var value []byte
			var err error
			value, err = c.AppRead(ctx, *app, uint32(*characteristic))
			if err != nil {
				return err
			}
			return json.NewEncoder(stdout).Encode(struct {
				App            string `json:"app"`
				Characteristic uint   `json:"characteristic"`
				Text           string `json:"text"`
				Bytes          []byte `json:"bytes_base64"`
			}{*app, *characteristic, string(value), value})
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
			before, err := c.Status(ctx)
			if err != nil {
				return err
			}
			plan, err := planManagement(ops[command], *app, before)
			if err != nil {
				return err
			}
			plans <- plan
			if err := c.Manage(ctx, ops[command], *app); err != nil {
				return err
			}
			status, err := c.Status(ctx)
			if err != nil {
				return err
			}
			managementResult <- status
			return nil
		}
	})
	if err != nil && ops[command] != 0 && operationCtx.Err() == nil {
		var rejected ble.DeviceError
		if !errors.As(err, &rejected) {
			select {
			case plan := <-plans:
				// A fresh authenticated read can prove completion after Windows
				// invalidates GATT handles. Never replay an uncertain mutation.
				checkErr := ble.WithClient(operationCtx, *id, token, func(ctx context.Context, c *ble.Client) error {
					status, e := c.Status(ctx)
					if e != nil {
						return e
					}
					if !plan.reached(status) {
						return errors.New("requested management state was not reached")
					}
					managementResult <- status
					return nil
				})
				if checkErr == nil {
					err = nil
				}
			default:
			}
		}
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintln(stderr, "BLE operation timed out; reconnect and query status before retrying activation")
		} else {
			fmt.Fprintln(stderr, err)
		}
		return 1
	}
	if ops[command] != 0 {
		status := <-managementResult
		select {
		case status = <-managementResult:
		default:
		}
		if json.NewEncoder(stdout).Encode(status) != nil {
			return 1
		}
	}
	return 0
}

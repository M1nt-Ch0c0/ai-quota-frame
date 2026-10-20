// Render one real source snapshot to disk without contacting the display.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
	_ "time/tzdata"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/config"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/dashboard"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/service"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/sources"
)

func run() error {
	output := flag.String("output", "frame.png", "output PNG")
	flag.Parse()
	c, err := config.FromEnv()
	if err != nil {
		return err
	}
	clients, err := sources.New(c)
	if err != nil {
		return err
	}
	clients.Quota.SetMaxConcurrency(c.MaxConcurrency)
	clients.Quota.SetPassiveMaxAge(c.PassiveMaxAge)
	svc := service.New(clients.Quota, c.RefreshInterval)
	if clients.Usage != nil {
		svc.SetUsageFetcher(clients.Usage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err = svc.Refresh(ctx); err != nil {
		return err
	}
	snapshot, ready := svc.Snapshot()
	if !ready || snapshot.Stale {
		return fmt.Errorf("no fresh snapshot")
	}
	renderer, err := dashboard.New(c.Location)
	if err != nil {
		return err
	}
	renderer.SetDisplayProviders(c.DisplayProviders)
	if err := renderer.SetTheme(c.FrameTheme); err != nil {
		return err
	}
	png, err := renderer.Render(snapshot)
	if err != nil {
		return err
	}
	if err = os.WriteFile(*output, png, 0600); err != nil {
		return err
	}
	fmt.Printf("Rendered %d bytes, %d accounts, stale=%v, usage=%v, warnings=%d\n", len(png), len(snapshot.Accounts), snapshot.Stale, snapshot.Usage != nil, len(snapshot.Errors))
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Command acat is the lightweight chat + video client.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"github.com/ape-dev-de/acat/internal/auth"
	"github.com/ape-dev-de/acat/internal/config"
	"github.com/ape-dev-de/acat/internal/demo"
	"github.com/ape-dev-de/acat/internal/single"
	"github.com/ape-dev-de/acat/internal/ui"
	"github.com/ape-dev-de/acat/internal/version"
)

// appID identifies acat to the OS (preferences, notifications).
const appID = "io.github.ape-dev-de.acat"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	demoMode := flag.Bool("demo", false, "run with built-in sample data (no sign-in)")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String("acat"))
		return
	}

	// One instance per user (demo mode may run alongside).
	var win atomic.Pointer[fyne.Window]
	if !*demoMode {
		if dir, err := config.Dir(); err == nil {
			inst, err := single.Acquire(dir, func() {
				fyne.Do(func() {
					if w := win.Load(); w != nil {
						(*w).Show()
						(*w).RequestFocus()
					}
				})
			})
			if errors.Is(err, single.ErrAlreadyRunning) {
				fmt.Fprintln(os.Stderr, "acat is already running; showing its window")
				return
			}
			defer inst.Release() // nil-safe if acquiring failed for another reason
		}
	}

	accts, err := accounts(*demoMode)
	if err != nil {
		fmt.Fprintln(os.Stderr, "acat:", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a := app.NewWithID(appID)
	u := ui.New(ctx, a, accts)
	u.Start()
	w := u.Window()
	win.Store(&w)
	w.ShowAndRun()
	if s, ok := accts.(interface{ StopCalls() }); ok {
		s.StopCalls()
	}
}

func accounts(demoMode bool) (ui.Accounts, error) {
	if demoMode {
		return demo.NewAccounts(), nil
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	cachePath, err := config.CachePath()
	if err != nil {
		return nil, err
	}
	m, err := auth.New(auth.Config{
		ClientID:  cfg.ClientID,
		CachePath: cachePath,
		Keys:      auth.KeyringStore{Service: "acat", User: "msal-cache-key"},
	})
	if err != nil {
		return nil, fmt.Errorf("%w\nSet ACAT_CLIENT_ID or client_id in the config file, or run 'acat -demo'", err)
	}
	return newMSALAccounts(m, cfg.RelayURL), nil
}

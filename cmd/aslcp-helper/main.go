// Command aslcp-helper is the privileged helper of ASLCP. It runs as root,
// socket-activated by systemd, and performs a fixed set of operations for the
// unprivileged aslcp server (see internal/helper).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/artw1/aslcp/internal/helper"
)

var version = "dev"

type uidList []uint32

func (u *uidList) String() string { return fmt.Sprint(*u) }
func (u *uidList) Set(s string) error {
	var n uint32
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return err
	}
	*u = append(*u, n)
	return nil
}

func main() {
	// The digital modes enable unit calls back into this binary for its plan,
	// config and check steps (internal/helper/dv.go).
	if len(os.Args) == 3 && os.Args[1] == "dv-step" {
		os.Exit(runStep(os.Args[2]))
	}
	// The port 80 revert timer (internal/helper/sys_webport.go).
	if len(os.Args) == 2 && os.Args[1] == "web-port-guard" {
		os.Exit(runGuard())
	}
	// One pass of the setup-hotspot watchdog, run by aslcp-wifi-watchdog.timer
	// (docs/DESIGN.md); it needs no running web server.
	if len(os.Args) == 2 && os.Args[1] == "wifi-watchdog" {
		if os.Geteuid() != 0 {
			fmt.Fprintln(os.Stderr, "wifi-watchdog must run as root")
			os.Exit(1)
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := helper.NewOps().RunWifiWatchdog(ctx, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "wifi-watchdog:", err)
			os.Exit(1)
		}
		return
	}
	// The session host of the browser terminal runs as the main process of
	// its own transient unit (internal/helper/termhost.go).
	if len(os.Args) >= 2 && os.Args[1] == "term-host" {
		os.Exit(helper.RunTermHost(os.Args[2:]))
	}
	listen := flag.String("listen", "", "listen on this unix socket path instead of using systemd socket activation (testing)")
	var allow uidList
	flag.Var(&allow, "allow-uid", "additionally allow this uid (testing; repeatable)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("aslcp-helper", version)
		return
	}
	logger := log.New(os.Stderr, "aslcp-helper: ", 0) // journald adds timestamps

	var ln net.Listener
	var err error
	if *listen != "" {
		ln, err = helper.ListenUnix(*listen)
	} else {
		ln, err = helper.ListenerFromSystemd()
		if err == nil && ln == nil {
			err = fmt.Errorf("not socket-activated (LISTEN_FDS unset); use --listen PATH for manual runs")
		}
	}
	if err != nil {
		logger.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() { <-ctx.Done(); ln.Close() }()

	// One-time move of the state directories of a 1.x install.
	if moved, err := helper.NewOps().MigrateDVState(); err != nil {
		logger.Printf("state directory migration: %v", err)
	} else {
		for _, m := range moved {
			logger.Printf("moved %s", m)
		}
	}
	srv := helper.NewServer(logger, allow...)
	logger.Printf("%s serving on %s", version, ln.Addr())
	if err := srv.Serve(ctx, ln); err != nil {
		logger.Fatal(err)
	}
}

// runStep runs one fixed step of the digital modes enable unit, as root, and exits with
// status 1 when it refuses.
func runStep(phase string) int {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "dv-step must run as root")
		return 1
	}
	o := helper.NewOps()
	if _, err := o.MigrateDVState(); err != nil {
		fmt.Fprintln(os.Stdout, "error:", err)
		return 1
	}
	if err := o.RunDVStep(context.Background(), phase, os.Stdout); err != nil {
		fmt.Fprintln(os.Stdout, "error:", err)
		return 1
	}
	return 0
}

// runGuard is the step of the transient revert timer started by the move to
// port 80: it undoes the move when nobody confirmed the new address.
func runGuard() int {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "web-port-guard must run as root")
		return 1
	}
	if err := helper.NewOps().WebPortGuard(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "web-port-guard:", err)
		return 1
	}
	return 0
}

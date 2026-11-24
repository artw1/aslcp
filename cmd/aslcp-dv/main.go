// Command aslcp-dv is the ASLCP digital voice engine: it bridges Asterisk's
// chan_usrp to digital voice networks, using md380-emu for AMBE+2.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// version is set at build time (-X main.version=...).
var version = "dev"

const usage = `usage: aslcp-dv <command> [flags]

commands:
  run       run the engine (systemd service)
  bench     measure vocoder throughput and latency (md380-emu, or -codec codec2)
  version   print the version
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(dispatch(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func dispatch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "run":
		return run(ctx, args[1:], stderr)
	case "bench":
		return bench(ctx, args[1:], stdout, stderr)
	case "version", "-version", "--version":
		fmt.Fprintln(stdout, "aslcp-dv", version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "aslcp-dv: unknown command %q\n%s", args[0], usage)
	return 2
}

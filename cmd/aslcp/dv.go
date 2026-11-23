package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/artw1/aslcp/internal/config"
	"github.com/artw1/aslcp/internal/helper"
)

const dvUsage = `Usage: aslcp dv <command> [options]

Manage the digital modes (DMR, YSF, P25, NXDN, M17) engine through the ASLCP
helper. enable and disable must run as root (sudo).

Commands:
  enable     install the engine's dependencies and start it; the job log is
             followed on standard output until the job ends
  disable    stop the engine and leave its configuration in place
  status     print the engine state (add --json for the raw status)

Options:
  --config PATH   configuration file (default ` + config.DefaultConfigPath + `)
  --json          status: print JSON

Exit codes: 0 done, 1 failed, 2 usage.
`

// dvClient is the part of the helper client the command uses.
type dvClient interface {
	DVStatus(ctx context.Context) (helper.DVStatus, error)
	DVEnable(ctx context.Context) (helper.DVStatus, error)
	DVDisable(ctx context.Context) (helper.DVStatus, error)
	DVJob(ctx context.Context) (helper.DVJob, error)
}

// dvEnv bundles the process-level inputs so tests can drive runDV.
type dvEnv struct {
	out, errw io.Writer
	isRoot    func() bool
	client    func(socket string) dvClient
	sleep     func(time.Duration)
	poll      time.Duration // between two reads of the job
	timeout   time.Duration // for the whole job
}

func dvMain(args []string) int {
	return runDV(args, dvEnv{
		out: os.Stdout, errw: os.Stderr,
		isRoot:  func() bool { return os.Geteuid() == 0 },
		client:  func(socket string) dvClient { return helper.NewClient(socket) },
		sleep:   time.Sleep,
		poll:    2 * time.Second,
		timeout: 60 * time.Minute,
	})
}

func runDV(args []string, env dvEnv) int {
	if len(args) == 0 {
		fmt.Fprint(env.errw, dvUsage)
		return exitUsage
	}
	cmd := args[0]
	switch cmd {
	case "-h", "--help", "help":
		fmt.Fprint(env.out, dvUsage)
		return exitOK
	case "install": // alias of the previous command name
		cmd = "enable"
	case "enable", "disable", "status":
	default:
		fmt.Fprintf(env.errw, "aslcp dv: unknown command %q\n\n%s", args[0], dvUsage)
		return exitUsage
	}
	fs := flag.NewFlagSet("aslcp dv "+cmd, flag.ContinueOnError)
	fs.SetOutput(env.errw)
	fs.Usage = func() { fmt.Fprint(env.errw, dvUsage) }
	cfgPath := fs.String("config", config.DefaultConfigPath, "path to aslcp.conf")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parseInterspersed(fs, args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(env.out, dvUsage)
			return exitOK
		}
		return exitUsage
	}
	if len(pos) != 0 {
		fmt.Fprintf(env.errw, "aslcp dv %s: unexpected argument %q\n\n%s", cmd, pos[0], dvUsage)
		return exitUsage
	}
	if cmd != "status" && !env.isRoot() {
		fmt.Fprintf(env.errw, "aslcp dv %s: must run as root (sudo aslcp dv %s)\n", cmd, cmd)
		return exitError
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(env.errw, "aslcp dv: %v\n", err)
		return exitError
	}
	c := env.client(cfg.HelperSocket)
	ctx := context.Background()
	switch cmd {
	case "status":
		st, err := c.DVStatus(ctx)
		if err != nil {
			fmt.Fprintf(env.errw, "aslcp dv status: cannot reach the helper: %v\n", err)
			return exitError
		}
		if *asJSON {
			b, _ := json.MarshalIndent(st, "", "  ")
			fmt.Fprintln(env.out, string(b))
			return exitOK
		}
		printDVStatus(env.out, st)
		return exitOK
	case "disable":
		if _, err := c.DVDisable(ctx); err != nil {
			fmt.Fprintf(env.errw, "aslcp dv disable: %v\n", err)
			return exitError
		}
		fmt.Fprintln(env.out, "Digital modes disabled.")
		return exitOK
	}
	if err := dvEnable(ctx, c, env); err != nil {
		fmt.Fprintf(env.errw, "aslcp dv enable: %v\n", err)
		return exitError
	}
	return exitOK
}

func printDVStatus(w io.Writer, st helper.DVStatus) {
	yn := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	fmt.Fprintf(w, "supported:  %s", yn(st.Supported))
	if st.Reason != "" {
		fmt.Fprintf(w, " (%s)", st.Reason)
	}
	fmt.Fprintf(w, "\nenabled:    %s\nactive:     %s\nmd380:      installed %s, active %s\n",
		yn(st.Enabled), yn(st.Active), yn(st.MD380Installed), yn(st.MD380Active))
	if st.Job != "" {
		fmt.Fprintf(w, "job:        %s\n", st.Job)
	}
	if len(st.LegacyUnits) > 0 {
		fmt.Fprintf(w, "legacy:     %s\n", strings.Join(st.LegacyUnits, ", "))
	}
}

// dvEnable starts the enable job (or joins one that is running) and follows
// its log until the job ends. It returns nil only when the job succeeded.
func dvEnable(ctx context.Context, c dvClient, env dvEnv) error {
	st, err := c.DVStatus(ctx)
	if err != nil {
		return fmt.Errorf("cannot reach the helper: %w", err)
	}
	seenRunning := st.Job == "running"
	var baseAt int64
	if !seenRunning {
		if !st.Supported {
			return fmt.Errorf("this host is not supported: %s", st.Reason)
		}
		fmt.Fprintln(env.out, "Enabling digital modes (this takes a few minutes)...")
		if _, err := c.DVEnable(ctx); err != nil {
			return err
		}
		baseAt = time.Now().Unix()
	} else {
		fmt.Fprintln(env.out, "Enabling is already running; following its log...")
	}
	return dvFollow(ctx, c, env, seenRunning, baseAt)
}

// dvFollow polls the job and prints the lines it has not printed yet. A
// terminal state counts only once the job was seen running, or is newer than
// the start (an older job's state must not end the wait).
func dvFollow(ctx context.Context, c dvClient, env dvEnv, seenRunning bool, baseAt int64) error {
	var printed []string
	deadline := time.Now().Add(env.timeout)
	began := time.Now()
	failures := 0
	for {
		j, err := c.DVJob(ctx)
		if err != nil {
			// the helper may restart; give up after a few tries in a row
			if failures++; failures >= 5 {
				return fmt.Errorf("lost the helper while following the job: %w", err)
			}
		} else {
			failures = 0
			lines := splitLog(j.Log)
			for _, l := range newLines(printed, lines) {
				fmt.Fprintln(env.out, l)
			}
			printed = lines
			switch j.Job {
			case "running":
				seenRunning = true
			case "done", "failed":
				if seenRunning || j.At > baseAt || time.Since(began) > 15*time.Second {
					if j.Job == "done" {
						fmt.Fprintln(env.out, "Digital modes enabled.")
						return nil
					}
					return errors.New("the job failed; see the log above")
				}
			}
		}
		if time.Now().After(deadline) {
			return errors.New("the job did not finish in time; check the digital modes page")
		}
		env.sleep(env.poll)
	}
}

func splitLog(s string) []string {
	s = strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// newLines returns the lines of cur after the longest suffix of prev that is
// a prefix of cur (the helper reports a sliding window of the journal).
func newLines(prev, cur []string) []string {
	for k := min(len(prev), len(cur)); k > 0; k-- {
		ok := true
		for i := 0; i < k; i++ {
			if prev[len(prev)-k+i] != cur[i] {
				ok = false
				break
			}
		}
		if ok {
			return cur[k:]
		}
	}
	return cur // nothing in common: print all
}

package main

import (
	"context"
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

const dvswitchUsage = `Usage: aslcp dvswitch install [options]

Install DVSwitch through the ASLCP helper: the same install as the DVSwitch
page (apt repository with a pinned key, the fixed package set, no recommends).
Must run as root (sudo). The install log is followed on standard output until
the job ends. DVSwitch settings saved from a backup restore are applied at
the end of the install.

Commands:
  install    install DVSwitch and follow the log

Options:
  --config PATH   configuration file (default ` + config.DefaultConfigPath + `)

Exit codes: 0 installed (or already installed), 1 failed, 2 usage.
`

// dvClient is the part of the helper client the command uses.
type dvClient interface {
	DVSwitchStatus(ctx context.Context) (helper.DVSwitchStatus, error)
	DVSwitchInstall(ctx context.Context) (helper.DVSwitchStatus, error)
	DVSwitchJob(ctx context.Context) (helper.DVSwitchJobs, error)
}

// dvEnv bundles the process-level inputs so tests can drive runDVSwitch.
type dvEnv struct {
	out, errw io.Writer
	isRoot    func() bool
	client    func(socket string) dvClient
	sleep     func(time.Duration)
	poll      time.Duration // between two reads of the job
	timeout   time.Duration // for the whole job
}

func dvswitchMain(args []string) int {
	return runDVSwitch(args, dvEnv{
		out: os.Stdout, errw: os.Stderr,
		isRoot:  func() bool { return os.Geteuid() == 0 },
		client:  func(socket string) dvClient { return helper.NewClient(socket) },
		sleep:   time.Sleep,
		poll:    2 * time.Second,
		timeout: 60 * time.Minute,
	})
}

func runDVSwitch(args []string, env dvEnv) int {
	if len(args) == 0 {
		fmt.Fprint(env.errw, dvswitchUsage)
		return exitUsage
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(env.out, dvswitchUsage)
		return exitOK
	case "install":
	default:
		fmt.Fprintf(env.errw, "aslcp dvswitch: unknown command %q\n\n%s", args[0], dvswitchUsage)
		return exitUsage
	}
	fs := flag.NewFlagSet("aslcp dvswitch install", flag.ContinueOnError)
	fs.SetOutput(env.errw)
	fs.Usage = func() { fmt.Fprint(env.errw, dvswitchUsage) }
	cfgPath := fs.String("config", config.DefaultConfigPath, "path to aslcp.conf")
	pos, err := parseInterspersed(fs, args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(env.out, dvswitchUsage)
			return exitOK
		}
		return exitUsage
	}
	if len(pos) != 0 {
		fmt.Fprintf(env.errw, "aslcp dvswitch install: unexpected argument %q\n\n%s", pos[0], dvswitchUsage)
		return exitUsage
	}
	if !env.isRoot() {
		fmt.Fprintln(env.errw, "aslcp dvswitch install: must run as root (sudo aslcp dvswitch install)")
		return exitError
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(env.errw, "aslcp dvswitch: %v\n", err)
		return exitError
	}
	if err := dvswitchInstall(context.Background(), env.client(cfg.HelperSocket), env); err != nil {
		if errors.Is(err, helper.ErrDVAlreadyInstalled) {
			fmt.Fprintln(env.out, "DVSwitch is already installed.")
			return exitOK
		}
		fmt.Fprintf(env.errw, "aslcp dvswitch install: %v\n", err)
		return exitError
	}
	return exitOK
}

// dvswitchInstall starts the install (or joins one that is running) and
// follows its log until the job ends. It returns nil only when the job
// succeeded, and ErrDVAlreadyInstalled when there is nothing to install.
func dvswitchInstall(ctx context.Context, c dvClient, env dvEnv) error {
	st, err := c.DVSwitchStatus(ctx)
	if err != nil {
		return fmt.Errorf("cannot reach the helper: %w", err)
	}
	if st.Install.Job != helper.JobRunning {
		if st.Installed && !st.CanInstall {
			return helper.ErrDVAlreadyInstalled
		}
		if !st.Supported {
			return fmt.Errorf("this host is not supported: %s", st.UnsupportedReason)
		}
		if st.RestorePending.Pending {
			fmt.Fprintf(env.out, "Settings saved from your backup (%s UTC) will be applied at the end of the install.\n", st.RestorePending.BackupDate)
		}
		fmt.Fprintln(env.out, "Starting the DVSwitch install (this takes a few minutes)...")
		started, err := c.DVSwitchInstall(ctx)
		if err != nil {
			return err
		}
		st = started
	} else {
		fmt.Fprintln(env.out, "A DVSwitch install is already running; following its log...")
	}
	baseAt := st.Install.At
	if baseAt == 0 && st.Install.Job != helper.JobRunning {
		baseAt = time.Now().Unix()
	}
	return dvswitchFollow(ctx, c, env, st.Install.Job == helper.JobRunning, baseAt)
}

// dvswitchFollow polls the job and prints the lines it has not printed yet.
// A terminal state counts only once the job was seen running, or is newer
// than the one at the start (an older job's state must not end the wait).
func dvswitchFollow(ctx context.Context, c dvClient, env dvEnv, seenRunning bool, baseAt int64) error {
	var printed []string
	deadline := time.Now().Add(env.timeout)
	began := time.Now()
	failures := 0
	for {
		j, err := c.DVSwitchJob(ctx)
		if err != nil {
			// the helper may restart; give up after a few tries in a row
			if failures++; failures >= 5 {
				return fmt.Errorf("lost the helper while following the install: %w", err)
			}
		} else {
			failures = 0
			in := j.Install
			lines := splitLog(in.Log)
			for _, l := range newLines(printed, lines) {
				fmt.Fprintln(env.out, l)
			}
			printed = lines
			if in.Job == helper.JobRunning {
				seenRunning = true
			} else if in.Job == helper.JobSucceeded || in.Job == helper.JobFailed {
				if seenRunning || in.At > baseAt || time.Since(began) > 15*time.Second {
					if in.Job == helper.JobSucceeded {
						fmt.Fprintln(env.out, "DVSwitch installed.")
						return nil
					}
					return errors.New("the DVSwitch install failed; see the log above")
				}
			}
		}
		if time.Now().After(deadline) {
			return errors.New("the DVSwitch install did not finish in time; check the DVSwitch page")
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

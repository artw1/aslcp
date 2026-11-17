package helper

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

// Runner executes fixed command lines. err is non-nil only when the command
// could not be run at all (not found, context expired); a non-zero exit
// status is reported through code with err == nil.
type Runner interface {
	Run(ctx context.Context, argv ...string) (stdout string, code int, err error)
}

// ExecRunner runs real commands (no shell).
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, argv ...string) (string, int, error) {
	if len(argv) == 0 {
		return "", -1, errors.New("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		// On failure the output includes stderr, for error messages.
		return out.String() + errb.String(), ee.ExitCode(), nil
	}
	if err != nil {
		return "", -1, err
	}
	return out.String(), 0, nil
}

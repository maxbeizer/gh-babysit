package process

import (
	"bytes"
	"context"
	"os/exec"
)

type Request struct {
	Name  string
	Args  []string
	Dir   string
	Env   []string
	Stdin []byte
}
type Result struct{ Stdout, Stderr []byte }
type Runner interface {
	Run(context.Context, Request) (Result, error)
}
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, r Request) (Result, error) {
	cmd := exec.CommandContext(ctx, r.Name, r.Args...)
	cmd.Dir = r.Dir
	cmd.Env = r.Env
	cmd.Stdin = bytes.NewReader(r.Stdin)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	return Result{Stdout: out.Bytes(), Stderr: stderr.Bytes()}, err
}

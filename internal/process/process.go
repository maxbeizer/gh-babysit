package process

import (
	"bytes"
	"context"
	"io"
	"os/exec"
)

type Request struct {
	Name                     string
	Args                     []string
	Dir                      string
	Env                      []string
	Stdin                    []byte
	StdoutLimit, StderrLimit int
}
type Result struct {
	Stdout, Stderr                   []byte
	StdoutTruncated, StderrTruncated bool
}
type Runner interface {
	Run(context.Context, Request) (Result, error)
}
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, r Request) (Result, error) {
	cmd := exec.CommandContext(ctx, r.Name, r.Args...)
	cmd.Dir = r.Dir
	cmd.Env = r.Env
	cmd.Stdin = bytes.NewReader(r.Stdin)
	stdout := newCapture(r.StdoutLimit)
	stderr := newCapture(r.StderrLimit)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	return Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), StdoutTruncated: stdout.Truncated(), StderrTruncated: stderr.Truncated()}, err
}

type capture interface {
	io.Writer
	Bytes() []byte
	Truncated() bool
}

func newCapture(limit int) capture {
	if limit <= 0 {
		return &unboundedCapture{}
	}
	return &boundedCapture{limit: limit}
}

type unboundedCapture struct{ bytes.Buffer }

func (c *unboundedCapture) Truncated() bool { return false }

type boundedCapture struct {
	limit      int
	head, tail []byte
	written    int
}

func (c *boundedCapture) Write(p []byte) (int, error) {
	n := len(p)
	c.written += n
	headLimit := (c.limit + 1) / 2
	if len(c.head) < headLimit {
		take := min(headLimit-len(c.head), len(p))
		c.head = append(c.head, p[:take]...)
		p = p[take:]
	}
	tailLimit := c.limit - headLimit
	if tailLimit > 0 && len(p) > 0 {
		c.tail = append(c.tail, p...)
		if len(c.tail) > tailLimit {
			c.tail = append(c.tail[:0], c.tail[len(c.tail)-tailLimit:]...)
		}
	}
	return n, nil
}
func (c *boundedCapture) Bytes() []byte {
	result := make([]byte, 0, len(c.head)+len(c.tail))
	result = append(result, c.head...)
	return append(result, c.tail...)
}
func (c *boundedCapture) Truncated() bool { return c.written > c.limit }

// Package browser drives one ego-browser Page through a small script that runs inside `ego-browser nodejs`.
//
// ego-browser exposes no CDP port and does not forward stdin to scripts, so the script connects back to this
// process over a Unix socket and serves page operations as JSON lines.
package browser

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/flazouh/ego-jev/internal/policy"
)

//go:embed bridge.js
var bridgeScript string

// ConfigMarker is replaced in bridge.js with the JSON config before the script starts.
const ConfigMarker = "__EGO_JEV_CONFIG__"

// Options says which ego task space and Page to drive.
type Options struct {
	// Space resumes a task space by id. When zero, ego resumes the space named Name, or creates it.
	Space int
	Name  string
	Page  string
	// Binary is the ego-browser executable. Defaults to "ego-browser" on PATH.
	Binary         string
	StartTimeout   time.Duration
	RequestTimeout time.Duration
}

// Config is what the bridge script receives in place of ConfigMarker.
type Config struct {
	Socket string `json:"socket"`
	Name   string `json:"name"`
	Page   string `json:"page"`
	Space  *int   `json:"space"`
}

// Info is what the bridge reports once connected.
type Info struct {
	SpaceID int    `json:"spaceId"`
	Page    string `json:"page"`
	URL     string `json:"url"`
}

type Bridge struct {
	Info Info

	cmd     *exec.Cmd
	conn    net.Conn
	reader  *bufio.Reader
	dir     string
	output  *cappedBuffer
	exited  chan error
	timeout time.Duration

	mu     sync.Mutex
	nextID int
	// broken is the first transport failure. After it, the connection may hold a late reply, so every call fails.
	broken error
}

type message struct {
	ID     int             `json:"id"`
	Method string          `json:"method,omitempty"`
	Params any             `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func (o *Options) defaults() {
	if o.Binary == "" {
		o.Binary = "ego-browser"
	}
	if o.Page == "" {
		o.Page = "p1"
	}
	if o.Name == "" {
		o.Name = "ego-jev"
	}
	if o.StartTimeout == 0 {
		o.StartTimeout = 30 * time.Second
	}
	if o.RequestTimeout == 0 {
		o.RequestTimeout = 60 * time.Second
	}
}

// Start launches the bridge script and waits for it to connect. Cancelling ctx aborts only the start;
// once connected, the bridge lives until Close so the task space can always be finished cleanly.
func Start(ctx context.Context, opts Options) (*Bridge, error) {
	opts.defaults()
	dir, err := os.MkdirTemp("", "ego-jev-")
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Bridge, error) {
		os.RemoveAll(dir)
		return nil, err
	}
	socket := filepath.Join(dir, "bridge.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return fail(err)
	}
	defer listener.Close()

	config := Config{Socket: socket, Name: opts.Name, Page: opts.Page}
	if opts.Space != 0 {
		config.Space = &opts.Space
	}
	configJSON, _ := json.Marshal(config)
	script := strings.Replace(bridgeScript, ConfigMarker, string(configJSON), 1)

	b := &Bridge{dir: dir, output: &cappedBuffer{limit: 64 << 10}, exited: make(chan error, 1), timeout: opts.RequestTimeout}
	b.cmd = exec.Command(opts.Binary, "nodejs", "-e", script)
	b.cmd.Stdout = b.output
	b.cmd.Stderr = b.output
	b.cmd.WaitDelay = 5 * time.Second
	if err := b.cmd.Start(); err != nil {
		return fail(fmt.Errorf("start %s: %w", opts.Binary, err))
	}
	go func() { b.exited <- b.cmd.Wait() }()

	accepted := make(chan net.Conn, 1)
	go func() {
		if conn, err := listener.Accept(); err == nil {
			accepted <- conn
		}
	}()
	kill := func(err error) (*Bridge, error) {
		b.cmd.Process.Kill()
		<-b.exited
		return fail(fmt.Errorf("%w: %s", err, b.output))
	}
	select {
	case b.conn = <-accepted:
	case err := <-b.exited:
		return fail(fmt.Errorf("ego-browser exited before the bridge connected (%v): %s", err, b.output))
	case <-time.After(opts.StartTimeout):
		return kill(fmt.Errorf("ego-browser bridge did not connect within %s", opts.StartTimeout))
	case <-ctx.Done():
		return kill(ctx.Err())
	}
	b.reader = bufio.NewReaderSize(b.conn, 1<<20)
	if err := b.call("hello", nil, &b.Info); err != nil {
		b.conn.Close()
		return kill(err)
	}
	return b, nil
}

func (b *Bridge) call(method string, params, result any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.broken != nil {
		return fmt.Errorf("%s: bridge unusable after an earlier failure: %w", method, b.broken)
	}
	b.nextID++
	id := b.nextID
	req, _ := json.Marshal(message{ID: id, Method: method, Params: params})
	b.conn.SetDeadline(time.Now().Add(b.timeout))
	if _, err := b.conn.Write(append(req, '\n')); err != nil {
		return b.lose(method, err)
	}
	line, err := b.reader.ReadBytes('\n')
	if err != nil {
		return b.lose(method, err)
	}
	var reply message
	if err := json.Unmarshal(line, &reply); err != nil {
		return b.lose(method, fmt.Errorf("unreadable reply: %w", err))
	}
	if reply.ID != id {
		return b.lose(method, fmt.Errorf("reply id %d does not match request id %d", reply.ID, id))
	}
	if reply.Error != "" {
		return fmt.Errorf("%s: %s", method, reply.Error)
	}
	if result == nil {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(reply.Result))
	dec.DisallowUnknownFields()
	if err := dec.Decode(result); err != nil {
		return fmt.Errorf("%s: bridge reply does not match the Go types: %w", method, err)
	}
	return nil
}

func (b *Bridge) lose(method string, err error) error {
	b.broken = fmt.Errorf("lost the ego-browser bridge during %s (%v): %s", method, err, b.output)
	return b.broken
}

func (b *Bridge) Goto(url string) error {
	return b.call("goto", map[string]string{"url": url}, nil)
}

func (b *Bridge) Observe(maxElements, maxText int) (policy.Observation, error) {
	var o policy.Observation
	err := b.call("observe", map[string]int{"maxElements": maxElements, "maxText": maxText}, &o)
	return o, err
}

// Perform runs one operation and waits for the page to settle. It returns how the input was sent.
func (b *Bridge) Perform(op policy.Op, target *policy.Target, text string, viewport policy.Viewport) (string, error) {
	var out struct {
		Via string `json:"via"`
	}
	err := b.call("perform", map[string]any{"op": op, "target": target, "text": text, "viewport": viewport}, &out)
	return out.Via, err
}

// MarkHeld returns a CSS selector for each held control, in order. The last observation tagged them.
func (b *Bridge) MarkHeld(ids []string) ([]string, error) {
	var out struct {
		Selectors []string `json:"selectors"`
	}
	err := b.call("markHeld", map[string]any{"ids": ids}, &out)
	return out.Selectors, err
}

// Close ends the bridge. With closeSpace, the ego task space is finished too; otherwise it stays open for the caller.
func (b *Bridge) Close(closeSpace bool) error {
	defer os.RemoveAll(b.dir)
	var callErr error
	b.mu.Lock()
	usable := b.broken == nil
	b.mu.Unlock()
	if usable {
		callErr = b.call("finish", map[string]bool{"close": closeSpace}, nil)
	} else if closeSpace {
		callErr = errors.New("the task space was not finished because the bridge was lost")
	}
	b.conn.Close()
	select {
	case err := <-b.exited:
		var exit *exec.ExitError
		if callErr == nil && errors.As(err, &exit) {
			return fmt.Errorf("ego-browser bridge exited with %d: %s", exit.ExitCode(), b.output)
		}
	case <-time.After(10 * time.Second):
		b.cmd.Process.Kill()
		<-b.exited
	}
	return callErr
}

// cappedBuffer keeps the first limit bytes of the bridge's output. exec copies into it from another goroutine.
type cappedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.limit - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(c.buf.String())
}

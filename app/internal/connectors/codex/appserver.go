package codex

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// AppServer is a minimal JSON-RPC client for `codex app-server` over stdio, kept alive and reused
// (starting one per delivery would re-run Codex's own startup writes).
type AppServer struct {
	Bin     string
	Env     map[string]string
	IdleMs  int64
	Timeout time.Duration

	callMu  sync.Mutex
	idleSeq uint64
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	ready   bool
	seq     int
	pending map[int]chan rpcMsg
	idle    *time.Timer
	stderr  []byte
	process *serverProcess
}

type rpcMsg struct {
	ID     *int            `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params map[string]any  `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Each launch owns its pipes and a completion channel. Stop waits for this exact
// generation, including descendants and readers, before a replacement can start.
type serverProcess struct {
	stdin    io.WriteCloser
	stdout   *os.File
	stderr   *os.File
	tree     *serverProcessTree
	done     chan struct{}
	stopOnce sync.Once
	stopping bool // protected by AppServer.mu
}

func (p *serverProcess) stop() {
	p.stopOnce.Do(func() {
		_ = p.stdin.Close()
		p.tree.kill()
	})
}

func (s *AppServer) start() error {
	for {
		s.mu.Lock()
		if s.process == nil {
			break
		}
		if s.ready && !s.process.stopping {
			s.mu.Unlock()
			return nil
		}
		done := s.process.done
		s.mu.Unlock()
		<-done
	}
	if s.pending == nil {
		s.pending = map[int]chan rpcMsg{}
	}
	cmd := exec.Command(s.Bin, "app-server")
	env := os.Environ()
	for k, v := range s.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	p, err := launchServer(cmd)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("cannot start %s app-server: %w", s.Bin, err)
	}
	s.cmd, s.stdin, s.stderr, s.process = cmd, p.stdin, nil, p
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		b := make([]byte, 4096)
		for {
			n, err := p.stderr.Read(b)
			s.mu.Lock()
			s.stderr = append(s.stderr, b[:n]...)
			if len(s.stderr) > 8192 {
				s.stderr = s.stderr[len(s.stderr)-8192:]
			}
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer readers.Done()
		sc := bufio.NewScanner(p.stdout)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			var m rpcMsg
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			s.mu.Lock()
			if m.ID != nil && m.Method == "" {
				if ch, ok := s.pending[*m.ID]; ok {
					delete(s.pending, *m.ID)
					ch <- m
				}
			}
			s.mu.Unlock()
		}
		// A broken/closed protocol stream cannot serve further requests.
		p.stop()
	}()
	go func() {
		// Do not wait for stdout EOF before reaping the launcher: descendants
		// may inherit its pipe handles and keep them open after it exits.
		_ = cmd.Wait()
		s.mu.Lock()
		p.stopping = true
		s.mu.Unlock()
		p.stop()
		p.tree.wait()
		readers.Wait()
		_ = p.stdout.Close()
		_ = p.stderr.Close()
		s.mu.Lock()
		why := "codex app-server exited: " + tail(string(s.stderr), 500)
		for id, ch := range s.pending {
			msg := rpcMsg{}
			msg.Error = &struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}{-1, why}
			ch <- msg
			delete(s.pending, id)
		}
		s.cmd, s.stdin, s.process, s.ready = nil, nil, nil, false
		close(p.done)
		s.mu.Unlock()
	}()
	s.ready = true
	s.mu.Unlock()
	r, err := s.raw("initialize", map[string]any{"clientInfo": map[string]any{"name": "koto", "version": "0.1.0"}, "capabilities": map[string]any{"experimentalApi": true}})
	if err == nil && r.Error != nil {
		err = fmt.Errorf("codex app-server initialize: %s", r.Error.Message)
	}
	if err == nil {
		err = s.write(map[string]any{"method": "initialized", "params": map[string]any{}})
	}
	if err != nil {
		s.Stop()
	}
	return err
}

func launchServer(cmd *exec.Cmd) (_ *serverProcess, err error) {
	var pipes []io.Closer
	defer func() {
		if err != nil {
			for _, pipe := range pipes {
				_ = pipe.Close()
			}
		}
	}()
	inRead, stdin, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	pipes = append(pipes, inRead, stdin)
	cmd.Stdin = inRead
	// Own these pipes rather than StdoutPipe/StderrPipe: cmd.Wait must be
	// independent of readers and must not close them before replies drain.
	stdout, outWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	pipes = append(pipes, stdout, outWrite)
	stderr, errWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	pipes = append(pipes, stderr, errWrite)
	cmd.Stdout, cmd.Stderr = outWrite, errWrite
	tree, err := startServerProcessTree(cmd)
	if err != nil {
		return nil, err
	}
	_ = inRead.Close()
	_ = outWrite.Close()
	_ = errWrite.Close()
	return &serverProcess{stdin: stdin, stdout: stdout, stderr: stderr, tree: tree, done: make(chan struct{})}, nil
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func (s *AppServer) write(v any) error {
	b, _ := json.Marshal(v)
	s.mu.Lock()
	w := s.stdin
	s.mu.Unlock()
	if w == nil {
		return errors.New("codex app-server is not running")
	}
	_, err := w.Write(append(b, '\n'))
	return err
}

func (s *AppServer) raw(method string, params map[string]any) (rpcMsg, error) {
	s.mu.Lock()
	s.seq++
	id := s.seq
	ch := make(chan rpcMsg, 1)
	s.pending[id] = ch
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	if err := s.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return rpcMsg{}, err
	}
	to := s.Timeout
	if to == 0 {
		to = 60 * time.Second
	}
	select {
	case m := <-ch:
		return m, nil
	case <-time.After(to):
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return rpcMsg{}, fmt.Errorf("%s: timeout", method)
	}
}

// Call a method and return its result.
func (s *AppServer) Call(method string, params map[string]any) (json.RawMessage, error) {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	s.mu.Lock()
	s.idleSeq++
	if s.idle != nil {
		s.idle.Stop()
	}
	s.mu.Unlock()
	defer s.touch()
	if err := s.start(); err != nil {
		return nil, err
	}
	m, err := s.raw(method, params)
	if err != nil {
		return nil, err
	}
	if m.Error != nil {
		return nil, fmt.Errorf("%s: %s", method, m.Error.Message)
	}
	return m.Result, nil
}

func (s *AppServer) touch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idle != nil {
		s.idle.Stop()
	}
	if s.process == nil || s.process.stopping {
		return
	}
	idle := time.Duration(s.IdleMs) * time.Millisecond
	if idle == 0 {
		idle = 60 * time.Second
	}
	s.idleSeq++
	seq := s.idleSeq
	s.idle = time.AfterFunc(idle, func() {
		s.callMu.Lock()
		defer s.callMu.Unlock()
		s.mu.Lock()
		valid := s.idleSeq == seq
		s.mu.Unlock()
		if valid {
			s.Stop()
		}
	})
}

// Stop ends only this launch's process tree and waits until it can no longer
// write Agent data. A subsequent Call may start a fresh server.
func (s *AppServer) Stop() {
	s.mu.Lock()
	s.idleSeq++
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
	p := s.process
	if p != nil {
		p.stopping = true
	}
	s.mu.Unlock()
	if p != nil {
		p.stop()
		<-p.done
	}
}

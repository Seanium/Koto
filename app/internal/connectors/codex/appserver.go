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

func (s *AppServer) start() error {
	s.mu.Lock()
	if s.ready {
		s.mu.Unlock()
		return nil
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
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		s.mu.Unlock()
		// Not on PATH (an app started from Finder gets a bare PATH) or not executable: fail the call.
		return fmt.Errorf("cannot start %s app-server: %w", s.Bin, err)
	}
	s.cmd, s.stdin, s.stderr = cmd, stdin, nil
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := stderr.Read(b)
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
		sc := bufio.NewScanner(stdout)
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
		_ = cmd.Wait()
		s.mu.Lock()
		if s.cmd == cmd {
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
			s.cmd, s.ready = nil, false
		}
		s.mu.Unlock()
	}()
	s.ready = true
	s.mu.Unlock()
	r, err := s.raw("initialize", map[string]any{"clientInfo": map[string]any{"name": "koto", "version": "0.1.0"}, "capabilities": map[string]any{"experimentalApi": true}})
	if err != nil {
		return err
	}
	if r.Error != nil {
		return fmt.Errorf("codex app-server initialize: %s", r.Error.Message)
	}
	return s.write(map[string]any{"method": "initialized", "params": map[string]any{}})
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

// Stop ends the server process.
func (s *AppServer) Stop() {
	s.mu.Lock()
	cmd := s.cmd
	if s.idle != nil {
		s.idle.Stop()
	}
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 30; i++ {
			s.mu.Lock()
			gone := s.cmd != cmd
			s.mu.Unlock()
			if gone {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		close(done)
	}()
	<-done
}

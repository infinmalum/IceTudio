package main

import (
	"errors"
	"strconv"
	"sync"
)

// DesktopService owns only the single desktop window's close handshake.
// Project state and Agent execution remain in their existing services.
type DesktopService struct {
	mu       sync.Mutex
	ready    bool
	approved bool
	serial   uint64
	pending  string
	emit     func(string)
	quit     func()
}

// Ready is called only after the frontend installs its close checks/listener.
func (s *DesktopService) Ready() {
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
}

// blockClose returns whether the native close must be cancelled. It cancels
// synchronously before emitting, and never calls platform code under the lock.
func (s *DesktopService) blockClose(cancel func()) bool {
	s.mu.Lock()
	if !s.ready || s.approved {
		s.mu.Unlock()
		return false
	}
	if s.pending == "" {
		s.serial++
		s.pending = strconv.FormatUint(s.serial, 10)
	}
	id := s.pending
	s.mu.Unlock()
	cancel()
	s.emit(id)
	return true
}

func (s *DesktopService) CancelClose(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || id != s.pending || s.approved {
		return errors.New("退出请求已过期；请重新关闭窗口")
	}
	s.pending = ""
	return nil
}

func (s *DesktopService) ConfirmClose(id string) error {
	s.mu.Lock()
	if id == "" || id != s.pending || s.approved {
		s.mu.Unlock()
		return errors.New("退出请求已过期；请重新关闭窗口")
	}
	s.approved = true
	s.pending = ""
	s.mu.Unlock()
	s.quit()
	return nil
}

package main

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestDesktopCloseHandshake(t *testing.T) {
	var order []string
	var quits int
	s := &DesktopService{emit: func(id string) { order = append(order, "emit:"+id) }, quit: func() { quits++ }}
	if s.blockClose(func() { t.Fatal("bootstrap close blocked") }) {
		t.Fatal("close before interactive frontend should be allowed")
	}
	s.Ready()
	request := func() {
		t.Helper()
		if !s.blockClose(func() { order = append(order, "cancel") }) {
			t.Fatal("interactive close bypassed frontend")
		}
	}
	request()
	request()
	if len(order) != 4 || order[0] != "cancel" || order[1] != "emit:1" || order[2] != "cancel" || order[3] != "emit:1" {
		t.Fatalf("cancellation/emission order or duplicate request: %v", order)
	}
	if s.ConfirmClose("unknown") == nil || quits != 0 {
		t.Fatal("unknown close ID authorized quit")
	}
	if err := s.CancelClose("1"); err != nil {
		t.Fatal(err)
	}
	request()
	if s.ConfirmClose("1") == nil || quits != 0 {
		t.Fatal("cancelled old request authorized new quit")
	}
	// Quit can reenter the platform's ShouldQuit path. No gate lock is held.
	s.quit = func() {
		quits++
		if s.blockClose(func() { t.Fatal("confirmed quit cancelled again") }) {
			t.Fatal("confirmed quit deadlocked/reopened handshake")
		}
	}
	if err := s.ConfirmClose("2"); err != nil || quits != 1 {
		t.Fatalf("confirm: %v quits=%d", err, quits)
	}
	if s.ConfirmClose("2") == nil || s.CancelClose("2") == nil || quits != 1 {
		t.Fatal("duplicate close confirmation was accepted")
	}
}

func TestDesktopConcurrentConfirmIsOneShot(t *testing.T) {
	var quits atomic.Int32
	s := &DesktopService{emit: func(string) {}, quit: func() { quits.Add(1) }}
	s.Ready()
	s.blockClose(func() {})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.ConfirmClose("1") }()
	}
	wg.Wait()
	if quits.Load() != 1 {
		t.Fatalf("quit called %d times", quits.Load())
	}
}

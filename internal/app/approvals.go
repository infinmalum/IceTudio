package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"icetudio/internal/storage"
)

// ApproveToolCall records the user's approval of one pending call. The
// approval binds to the exact persisted call row and, via the store's
// argument hash, to its exact arguments — it can never be reused for a
// different call or changed parameters.
func (s *Service) ApproveToolCall(callRowID string) (storage.ToolCall, error) {
	if err := s.checkReady(); err != nil {
		return storage.ToolCall{}, err
	}
	call, run, err := s.callWithRun(callRowID)
	if err != nil {
		return storage.ToolCall{}, err
	}
	if call.State != storage.CallPending {
		return storage.ToolCall{}, fmt.Errorf("工具调用状态 %q 不在待审批状态", call.State)
	}
	if run.Status != "running" {
		return storage.ToolCall{}, errors.New("任务已结束，不能再审批")
	}
	if err := s.store.ApproveToolCall(context.Background(), callRowID, storage.ApprovalUser); err != nil {
		return storage.ToolCall{}, err
	}
	s.signalApproval(callRowID)
	return s.store.GetToolCall(context.Background(), callRowID)
}

// DenyToolCall records the user's refusal of one pending call. The denial is
// terminal; the model sees it as a tool error and can adapt.
func (s *Service) DenyToolCall(callRowID, note string) (storage.ToolCall, error) {
	if err := s.checkReady(); err != nil {
		return storage.ToolCall{}, err
	}
	call, _, err := s.callWithRun(callRowID)
	if err != nil {
		return storage.ToolCall{}, err
	}
	if call.State != storage.CallPending {
		return storage.ToolCall{}, fmt.Errorf("工具调用状态 %q 不在待审批状态", call.State)
	}
	if err := s.store.DenyToolCall(context.Background(), callRowID, note); err != nil {
		return storage.ToolCall{}, err
	}
	s.signalApproval(callRowID)
	return s.store.GetToolCall(context.Background(), callRowID)
}

// callWithRun resolves the persisted call, run and session relationship.
func (s *Service) callWithRun(callRowID string) (storage.ToolCall, storage.Run, error) {
	call, err := s.store.GetToolCall(context.Background(), callRowID)
	if err != nil {
		return storage.ToolCall{}, storage.Run{}, err
	}
	run, err := s.store.GetRun(context.Background(), call.RunID)
	if err != nil {
		return storage.ToolCall{}, storage.Run{}, err
	}
	if err := s.checkSessionExists(run.SessionID); err != nil {
		return storage.ToolCall{}, storage.Run{}, err
	}
	return call, run, nil
}

// WaitApproval implements agent.ApprovalWaiter for the run loop. The
// persisted call state is the single source of truth: approval and denial
// are persisted first and only then signaled, so a lost signal at worst
// delays the decision until the next poll.
func (s *Service) WaitApproval(ctx context.Context, callRowID string) (bool, string, error) {
	for {
		call, err := s.store.GetToolCall(ctx, callRowID)
		if err != nil {
			return false, "", err
		}
		switch call.State {
		case storage.CallApproved:
			return true, "", nil
		case storage.CallFailed:
			note := call.Error
			if strings.TrimSpace(note) == "" {
				note = "用户拒绝执行"
			}
			return false, note, nil
		case storage.CallCancelled:
			note := call.Error
			if strings.TrimSpace(note) == "" {
				note = "调用已取消"
			}
			return false, note, nil
		case storage.CallExecuting, storage.CallSucceeded:
			// Already dispatched through another path; treat as approved.
			return true, "", nil
		}
		ch := s.approvalChan(callRowID)
		ticker := time.NewTicker(200 * time.Millisecond)
		select {
		case <-ch:
		case <-ticker.C:
		case <-ctx.Done():
			ticker.Stop()
			s.dropApprovalChan(callRowID)
			return s.settleExpiredWait(ctx, callRowID)
		}
		ticker.Stop()
	}
}

// settleExpiredWait converges the two ways a wait can end: the approval
// window timed out (persist the expiry; a just-arrived approval wins the
// race) or the run was cancelled (the run's sweep already recorded it).
func (s *Service) settleExpiredWait(ctx context.Context, callRowID string) (bool, string, error) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err := s.store.CancelPendingCall(context.WithoutCancel(ctx), callRowID, "审批超时，调用未执行")
		if err != nil {
			// The state may have moved on (e.g. the user approved at the
			// last moment); the store decides, not the timer.
			call, getErr := s.store.GetToolCall(context.Background(), callRowID)
			if getErr == nil && call.State == storage.CallApproved {
				return true, "", nil
			}
			return false, "", fmt.Errorf("记录审批超时失败: %w", err)
		}
		return false, "审批超时，调用未执行", nil
	}
	return false, "任务已取消", nil
}

func (s *Service) approvalChan(callRowID string) chan struct{} {
	s.muApprovals.Lock()
	defer s.muApprovals.Unlock()
	ch, ok := s.waiters[callRowID]
	if !ok {
		ch = make(chan struct{})
		s.waiters[callRowID] = ch
	}
	return ch
}

func (s *Service) signalApproval(callRowID string) {
	s.muApprovals.Lock()
	defer s.muApprovals.Unlock()
	if ch, ok := s.waiters[callRowID]; ok {
		close(ch)
		delete(s.waiters, callRowID)
	}
}

func (s *Service) dropApprovalChan(callRowID string) {
	s.muApprovals.Lock()
	defer s.muApprovals.Unlock()
	delete(s.waiters, callRowID)
}

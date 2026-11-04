package desktop

import (
	"errors"
	"fmt"
	"sync"

	"icetudio/internal/app"
	"icetudio/internal/project"
)

// editBackend is synchronous and in-process. Do not implement it with a remote
// transport: only a returned local command plus a durable query can establish
// a rejected transaction. A lost UI reply is recovered through the journal.
type editBackend interface {
	GetPendingProjectEdit() (*app.ProjectEditRecovery, error)
	BeginProjectEdit(string, string, int64, string) error
	ClearPendingProjectEdit(string, string) error
	SubmitProjectOps(string, string, string, int64, []project.Op) (project.Result, error)
	UndoProject(string, string, string, int64) (project.Result, error)
	RedoProject(string, string, string, int64) (project.Result, error)
	GetProjectTransaction(string, string) (project.Result, bool, error)
}

// EditState is a presentation of the existing durable journal, not project
// state or another undo stack. Its revision is local UI state for close checks.
type EditState struct {
	Version uint64
	Ready   bool
	Busy    bool
	Pending *app.ProjectEditRecovery
	Message string
}

type EditTarget struct {
	ProjectID string
	Revision  int64
}

// Edits coordinates the one shared desktop intention slot across Native and
// WebView writes. It delegates all music semantics to app.Service.
type Edits struct {
	mu      sync.Mutex
	backend editBackend
	state   EditState
}

func NewEdits(svc *app.Service) *Edits { return &Edits{backend: svc} }
func (e *Edits) State() EditState {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.state
	if s.Pending != nil {
		record := *s.Pending
		s.Pending = &record
	}
	return s
}
func (e *Edits) start(check bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state.Busy || (!check && (!e.state.Ready || e.state.Pending != nil)) {
		return errors.New("工程操作不可用，请先等待或核对原保存结果")
	}
	e.state.Busy = true
	e.state.Version++
	return nil
}
func (e *Edits) finish() { e.mu.Lock(); e.state.Busy = false; e.state.Version++; e.mu.Unlock() }
func (e *Edits) set(ready bool, pending *app.ProjectEditRecovery, message string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state.Ready = ready
	e.state.Pending = pending
	e.state.Message = message
	e.state.Version++
}

// Check may retry failed reads but cannot overwrite an active submission.
func (e *Edits) Check() error {
	if err := e.start(true); err != nil {
		return err
	}
	defer e.finish()
	record, err := e.backend.GetPendingProjectEdit()
	if err != nil {
		e.set(false, nil, "恢复检查失败，音乐编辑暂不可用")
		return err
	}
	message := "工程恢复检查完成"
	if record != nil {
		message = "发现原工程待确认操作；请查询保存结果，不会重放"
	}
	e.set(true, record, message)
	return nil
}

// Execute requires a target frozen by the caller before any asynchronous work.
// The caller must keep ops immutable until this method returns.
func (e *Edits) Execute(target EditTarget, kind string, ops []project.Op) (project.Result, error) {
	return e.execute(target, kind, ops, project.NewID("op"), false)
}

// ExecutePending leaves the durable intention until the WebView acknowledges
// the returned commit. A lost bridge reply cannot silently clear recovery.
func (e *Edits) ExecutePending(target EditTarget, kind string, ops []project.Op, opID string) (project.Result, error) {
	return e.execute(target, kind, ops, opID, true)
}
func (e *Edits) execute(target EditTarget, kind string, ops []project.Op, opID string, pendingAck bool) (project.Result, error) {
	if kind != "edit" && kind != "undo" && kind != "redo" {
		return project.Result{}, errors.New("不支持的桌面编辑类型")
	}
	if err := e.start(false); err != nil {
		return project.Result{}, err
	}
	defer e.finish()
	record := &app.ProjectEditRecovery{Version: 1, ProjectID: target.ProjectID, BaseRevision: target.Revision, OpID: opID, Kind: kind}
	e.set(true, record, "正在保存…")
	if err := e.backend.BeginProjectEdit(record.ProjectID, record.OpID, target.Revision, kind); err != nil {
		stored, readErr := e.backend.GetPendingProjectEdit()
		if readErr != nil {
			e.set(false, record, "无法核对恢复标记；编辑未发送")
			return project.Result{}, fmt.Errorf("编辑未发送：%w；读取恢复标记：%v", err, readErr)
		}
		e.set(true, stored, "编辑未发送：恢复标记未获确认")
		return project.Result{}, err
	}
	var result project.Result
	var err error
	switch kind {
	case "edit":
		result, err = e.backend.SubmitProjectOps(target.ProjectID, record.OpID, "gui", target.Revision, ops)
	case "undo":
		result, err = e.backend.UndoProject(target.ProjectID, record.OpID, "gui", target.Revision)
	case "redo":
		result, err = e.backend.RedoProject(target.ProjectID, record.OpID, "gui", target.Revision)
	}
	if err == nil {
		return e.recordCommit(record, result, pendingAck)
	}
	result, found, queryErr := e.backend.GetProjectTransaction(record.ProjectID, record.OpID)
	if queryErr != nil {
		e.set(true, record, "保存结果未知，请查询原操作")
		return project.Result{}, fmt.Errorf("保存返回错误：%w；查询失败：%v", err, queryErr)
	}
	if found {
		return e.recordCommit(record, result, pendingAck)
	}
	// The in-process synchronous method has finished; no remote request can be
	// left running. Absence after a bridge interruption alone is NOT this case.
	if clearErr := e.backend.ClearPendingProjectEdit(record.ProjectID, record.OpID); clearErr != nil {
		e.set(true, record, "编辑已拒绝，恢复标记待清理")
		return project.Result{}, fmt.Errorf("%w；清理失败：%v", err, clearErr)
	}
	e.set(true, nil, "编辑已拒绝，未覆盖新修订")
	return project.Result{}, err
}
func (e *Edits) recordCommit(record *app.ProjectEditRecovery, result project.Result, pendingAck bool) (project.Result, error) {
	if pendingAck {
		e.set(true, record, fmt.Sprintf("已保存 · 修订 %d；等待卷帘确认", result.Revision))
		return result, nil
	}
	return e.committed(record, result)
}

// Acknowledge confirms the original durable result, never another intention.
func (e *Edits) Acknowledge(id, opID string) error {
	if err := e.start(true); err != nil {
		return err
	}
	defer e.finish()
	s := e.State()
	if s.Pending != nil && (s.Pending.ProjectID != id || s.Pending.OpID != opID) {
		return errors.New("原操作已变化，未清除其他恢复标记")
	}
	result, found, err := e.backend.GetProjectTransaction(id, opID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("尚未确认原操作提交，恢复标记保留")
	}
	if s.Pending == nil {
		return nil
	}
	_, err = e.committed(s.Pending, result)
	return err
}
func (e *Edits) committed(record *app.ProjectEditRecovery, result project.Result) (project.Result, error) {
	message := fmt.Sprintf("已保存 · 修订 %d", result.Revision)
	if err := e.backend.ClearPendingProjectEdit(record.ProjectID, record.OpID); err != nil {
		e.set(true, record, message+"；恢复标记清理失败，请核对")
		return result, nil
	}
	e.set(true, nil, message)
	return result, nil
}

// Query never replays a command. found=false keeps the original journal.
func (e *Edits) Query() (project.Result, bool, error) {
	if err := e.start(true); err != nil {
		return project.Result{}, false, err
	}
	defer e.finish()
	s := e.State()
	if !s.Ready || s.Pending == nil {
		return project.Result{}, false, errors.New("请先完成恢复检查")
	}
	result, found, err := e.backend.GetProjectTransaction(s.Pending.ProjectID, s.Pending.OpID)
	if err != nil {
		return result, false, err
	}
	if found {
		result, err = e.committed(s.Pending, result)
		return result, true, err
	}
	e.set(true, s.Pending, "尚未查到原操作提交；保留待核对状态，不会重放")
	return result, false, nil
}

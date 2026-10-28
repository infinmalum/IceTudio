package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"icetudio/internal/config"
	"icetudio/internal/materials"
	"icetudio/internal/storage"
)

// Runs owns cancellation signals for active tasks.
type Runs struct {
	store  *storage.Store
	mu     sync.Mutex
	active map[string]context.CancelFunc
}

const RunTimeout = 10 * time.Minute

func NewRuns(store *storage.Store) *Runs {
	return &Runs{store: store, active: map[string]context.CancelFunc{}}
}

// Start creates the run and resolves the conversation's current RuntimeConfig
// (执行配置) in one transaction. The returned snapshot is the configuration the
// run must execute with; later config edits affect only subsequent runs.
func (r *Runs) Start(ctx context.Context, sessionID string) (storage.Run, context.Context, storage.RunConfigSnapshot, error) {
	return r.start(ctx, sessionID, storage.CreateRunOptions{})
}

// StartPrepared creates the run with a resolved model and skill snapshot.
// projectScope freezes the run's project edit range (docs/p3-agent-scope.md);
// an empty request captures the session binding in the creation transaction.
func (r *Runs) StartPrepared(ctx context.Context, sessionID string, model config.Model, snapshot storage.SkillSnapshotter, projectScope string, searchTools ...string) (storage.Run, context.Context, storage.RunConfigSnapshot, error) {
	return r.start(ctx, sessionID, storage.CreateRunOptions{DefaultModel: model, SnapshotSkills: snapshot, SearchTools: searchTools, ProjectScope: projectScope})
}

func (r *Runs) StartInspection(ctx context.Context, sessionID, target string, retrospective bool, source config.Model, snapshot storage.SkillSnapshotter) (storage.Run, context.Context, storage.RunConfigSnapshot, error) {
	return r.start(ctx, sessionID, storage.CreateRunOptions{DefaultModel: source, SnapshotSkills: snapshot, Inspection: &storage.InspectionRequest{TargetRunID: target, Retrospective: retrospective}})
}

func (r *Runs) StartImprover(ctx context.Context, session string, source config.Model) (storage.Run, context.Context, storage.RunConfigSnapshot, error) {
	return r.start(ctx, session, storage.CreateRunOptions{Component: "improver", DefaultModel: source})
}

func (r *Runs) StartWorker(ctx context.Context, sessionID, parentID string, depth int, name string) (storage.Run, context.Context, storage.RunConfigSnapshot, error) {
	return r.start(ctx, sessionID, storage.CreateRunOptions{ParentRunID: parentID, Depth: depth, WorkerName: name})
}

func (r *Runs) StartPlannedWorker(ctx context.Context, sessionID, parentID string, spec storage.TaskSpec) (storage.Run, context.Context, storage.RunConfigSnapshot, error) {
	return r.start(ctx, sessionID, storage.CreateRunOptions{ParentRunID: parentID, Depth: 1, WorkerName: spec.Worker, TaskSpec: &spec})
}

func (r *Runs) StartPlanner(ctx context.Context, session, parent string) (storage.Run, context.Context, storage.RunConfigSnapshot, error) {
	return r.start(ctx, session, storage.CreateRunOptions{ParentRunID: parent, Depth: 1, WorkerName: "@planner", Component: "planner"})
}

func (r *Runs) StartReviewer(ctx context.Context, session, parent string) (storage.Run, context.Context, storage.RunConfigSnapshot, error) {
	return r.start(ctx, session, storage.CreateRunOptions{ParentRunID: parent, Depth: 1, WorkerName: "@reviewer", Component: "reviewer"})
}

func (r *Runs) start(ctx context.Context, sessionID string, opts storage.CreateRunOptions) (storage.Run, context.Context, storage.RunConfigSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return storage.Run{}, nil, storage.RunConfigSnapshot{}, err
	}
	item, resolved, err := r.store.CreateRunWithOptions(ctx, sessionID, opts)
	if err != nil {
		return storage.Run{}, nil, storage.RunConfigSnapshot{}, err
	}
	timeout := RunTimeout
	if opts.ParentRunID == "" {
		timeout = time.Duration(resolved.Execution.Budget.Effective().MaxSeconds) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	r.active[item.ID] = cancel
	return item, runCtx, resolved, nil
}

func (r *Runs) ContinueFixed(ctx context.Context, root, task, input string, files []materials.File) (context.Context, storage.RunConfigSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, active := r.active[root]; active {
		return nil, storage.RunConfigSnapshot{}, errors.New("任务执行尚未结束")
	}
	if err := ctx.Err(); err != nil {
		return nil, storage.RunConfigSnapshot{}, err
	}
	p, remaining, err := r.store.ContinueFixedRun(ctx, root, task, input, files)
	if err != nil {
		return nil, p, err
	}
	runCtx, cancel := context.WithTimeout(ctx, remaining)
	r.active[root] = cancel
	return runCtx, p, nil
}

func (r *Runs) Cancel(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cancel, ok := r.active[id]
	if !ok {
		return errors.New("任务未运行")
	}
	if err := r.store.SetRunStatus(context.Background(), id, "cancelled", ""); err != nil {
		return err
	}
	cancel()
	delete(r.active, id)
	return nil
}

func (r *Runs) Finish(id, status, detail string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cancel, ok := r.active[id]
	if !ok {
		return errors.New("任务未运行")
	}
	if err := r.store.SetRunStatus(context.Background(), id, status, detail); err != nil {
		return err
	}
	cancel()
	delete(r.active, id)
	return nil
}

func (r *Runs) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, cancel := range r.active {
		_ = r.store.SetRunStatus(context.Background(), id, "interrupted", "应用已关闭")
		cancel()
		delete(r.active, id)
	}
}

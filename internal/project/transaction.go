package project

import (
	"encoding/json"
	"fmt"
	"slices"
)

// clone copies the state through its JSON representation; the wire format
// is the storage format, so this is lossless by construction.
func clone(st *State) (*State, error) {
	raw, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	var out State
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MaxOpsPerTransaction bounds one batch; larger intents must be split into
// multiple transactions by the caller.
const MaxOpsPerTransaction = 1000

// Result is the durable outcome of one committed transaction. It is stored
// with the history row so a caller that lost its response can query it by
// opID instead of replaying the request.
type Result struct {
	OpID         string         `json:"opId"`
	Revision     int64          `json:"revision"`
	BaseRevision int64          `json:"baseRevision"`
	Source       string         `json:"source"`
	Applied      int            `json:"applied"`
	Summary      map[string]int `json:"summary"`
	CreatedIDs   []string       `json:"createdIds,omitempty"`
}

// HistoryEntry is one committed transaction in the project's linear
// history. Ops holds what was applied; Inverse holds how to undo it. For
// undo entries Ops is the inverse that was applied; for redo entries Ops is
// the target edit's ops re-applied.
type HistoryEntry struct {
	Revision       int64  `json:"revision"`
	OpID           string `json:"opId"`
	Source         string `json:"source"`
	BaseRevision   int64  `json:"baseRevision"`
	Kind           string `json:"kind"` // edit | undo | redo
	TargetRevision int64  `json:"targetRevision,omitempty"`
	Ops            []Op   `json:"ops,omitempty"`
	Inverse        []Op   `json:"inverse,omitempty"`
	Result         Result `json:"result"`
	Undone         bool   `json:"undone"`
	CreatedAt      string `json:"createdAt"`
}

// ApplyTransaction validates and applies a batch of ops, returning the
// inverse batch and the result body (without revision, which the storage
// layer assigns). The batch is atomic: the state is only replaced when
// every op succeeds; on error st keeps its previous content exactly.
func ApplyTransaction(st *State, ops []Op) (inverse []Op, result Result, err error) {
	_, inverse, result, err = ApplyTransactionWithHistory(st, ops)
	return inverse, result, err
}

// ApplyTransactionWithHistory also returns the operations actually applied,
// including IDs assigned to creations. Persist these operations for redo:
// persisting the original request would allocate different IDs on replay.
// This permits internal restoration payloads and identity reuse for trusted
// history execution. Untrusted fresh edits must use ApplyNewTransaction.
// Neither the request nor the state is modified on failure.
func ApplyTransactionWithHistory(st *State, ops []Op) (applied, inverse []Op, result Result, err error) {
	return applyTransaction(st, ops, nil, false)
}

// ApplyNewTransaction is the fresh-edit path. Prior IDs are scoped to this
// project, including objects no longer alive. History execution instead uses
// ApplyTransactionWithHistory so undo/redo can restore the same identities.
// The supplied reservation set is read-only; rejected batches reserve nothing.
func ApplyNewTransaction(st *State, ops []Op, priorIDs map[string]bool) (applied, inverse []Op, result Result, err error) {
	return applyTransaction(st, ops, priorIDs, true)
}

func applyTransaction(st *State, ops []Op, priorIDs map[string]bool, fresh bool) (applied, inverse []Op, result Result, err error) {
	if len(ops) == 0 {
		return nil, nil, result, fmt.Errorf("事务不能为空")
	}
	if len(ops) > MaxOpsPerTransaction {
		return nil, nil, result, fmt.Errorf("一次事务最多 %d 个操作，收到 %d", MaxOpsPerTransaction, len(ops))
	}
	working, err := clone(st)
	if err != nil {
		return nil, nil, result, fmt.Errorf("工程状态无法复制：%w", err)
	}
	ids := idSet(working)
	var used map[string]bool
	if fresh {
		used = idSet(working)
		for id, reserved := range priorIDs {
			if reserved {
				used[id] = true
			}
		}
	}
	summary := map[string]int{}
	for _, op := range ops {
		if fresh && (len(op.InvClips) != 0 || len(op.InvNotes) != 0 || len(op.InvControls) != 0 || len(op.InvRaw) != 0) {
			return nil, nil, Result{}, opError(op.Kind, "普通编辑不能携带内部恢复载荷；恢复原对象请使用撤销/重做")
		}
		inv, created, err := applyOp(working, ids, op)
		if err != nil {
			return nil, nil, Result{}, err
		}
		if fresh {
			for _, id := range created {
				if used[id] {
					return nil, nil, Result{}, opError(op.Kind, "对象 ID %s 已使用，不能分配给新对象；请使用新 ID，恢复原对象请使用撤销/重做", id)
				}
				used[id] = true // Never removed, even if a later op deletes it.
			}
		}
		applied = append(applied, withCreatedIDs(op, created))
		inverse = append(inverse, inv)
		summary[op.Kind]++
		result.CreatedIDs = append(result.CreatedIDs, created...)
	}
	if rawByteCount(working) > MaxRawBytes {
		return nil, nil, Result{}, fmt.Errorf("原始事件字节总量超过上限 %d", MaxRawBytes)
	}
	// Undo later changes before the operations they depend on (for example,
	// remove a newly added note before shrinking its extended clip).
	slices.Reverse(inverse)
	*st = *working
	result.Summary = summary
	result.Applied = len(ops)
	return applied, inverse, result, nil
}

// applyOp returns creation IDs in input order (including explicit IDs).
// Copy only the fields we fill so caller-owned pointers/slices stay untouched.
func withCreatedIDs(op Op, ids []string) Op {
	switch op.Kind {
	case "track.add":
		spec := *op.Track
		spec.ID = ids[0]
		op.Track = &spec
	case "clip.add":
		spec := *op.Clip
		spec.ID = ids[0]
		op.Clip = &spec
	case "note.add":
		op.Notes = slices.Clone(op.Notes)
		for i, id := range ids {
			op.Notes[i].ID = id
		}
	case "control.add":
		op.Events = slices.Clone(op.Events)
		for i, id := range ids {
			op.Events[i].ID = id
		}
	}
	return op
}

// PlanUndo finds the transaction to undo and the ops that reverse it. The
// newest not-yet-undone edit is the target; state must be the current
// state.
func PlanUndo(history []HistoryEntry) (HistoryEntry, []Op, error) {
	for i := len(history) - 1; i >= 0; i-- {
		h := history[i]
		if h.Kind == EditKind && !h.Undone {
			if len(h.Inverse) == 0 {
				return h, nil, fmt.Errorf("历史记录缺少逆操作，无法撤销修订 %d", h.Revision)
			}
			return h, h.Inverse, nil
		}
	}
	return HistoryEntry{}, nil, fmt.Errorf("没有可撤销的操作")
}

// PlanRedo re-applies the original ops of the newest undo that is still
// pending. Undos stacked on top of the last edit are candidates (newest
// first); an undo already re-applied by a redo entry is consumed; any
// undo below the last edit is stale. A new edit after an undo therefore
// invalidates redo, matching common sequencer behavior.
func PlanRedo(history []HistoryEntry) (HistoryEntry, []Op, error) {
	lastEdit := -1
	for i, h := range history {
		if h.Kind == EditKind {
			lastEdit = i
		}
	}
	for i := len(history) - 1; i > lastEdit; i-- {
		h := history[i]
		if h.Kind != UndoKind {
			continue
		}
		consumed := false
		for _, r := range history {
			if r.Kind == RedoKind && r.TargetRevision == h.Revision {
				consumed = true
				break
			}
		}
		if consumed {
			continue
		}
		for _, e := range history {
			if e.Revision == h.TargetRevision {
				if len(e.Ops) == 0 {
					return h, nil, fmt.Errorf("历史记录缺少原操作，无法重做修订 %d", h.TargetRevision)
				}
				return h, e.Ops, nil
			}
		}
		return h, nil, fmt.Errorf("重做目标修订 %d 不存在", h.TargetRevision)
	}
	return HistoryEntry{}, nil, fmt.Errorf("没有可重做的操作")
}

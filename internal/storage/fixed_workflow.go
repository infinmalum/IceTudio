package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Fixed stages are configuration, never model-maintained plans. All stages are
// required; an explicit input prompt gates execution until the user answers.
func validateFixedWorkflow(data RuntimeConfigData) error {
	e := data.Execution
	if len(e.Stages) == 0 && e.Workflow != "fixed" {
		return nil
	}
	if len(e.Stages) == 0 || len(e.Stages) > 20 {
		return errors.New("固定流程需要 1 到 20 个阶段")
	}
	slots := map[string]bool{}
	for _, w := range e.WorkerSources {
		slots[w.Name] = true
	}
	steps := make([]PlanStep, 0, len(e.Stages))
	total := 0
	for _, stage := range e.Stages {
		if !planID.MatchString(stage.ID) || strings.TrimSpace(stage.Task) == "" || len(stage.Task) > 10000 || len(stage.Materials) > 65536 {
			return errors.New("固定阶段需要有效 ID、任务描述（最多 10000 字节）和材料（最多 65536 字节）")
		}
		if len(stage.InputPrompt) > 4000 || (stage.InputPrompt != "" && strings.TrimSpace(stage.InputPrompt) == "") {
			return errors.New("阶段输入提示需为非空文本且不超过 4000 字节")
		}
		if !slots[stage.Worker] {
			return errors.New("固定阶段必须引用已配置的 worker 槽位")
		}
		if stage.AccessMode != "read" && stage.AccessMode != "write" && stage.AccessMode != "unknown" {
			return errors.New("固定阶段必须明确 accessMode：read、write 或 unknown")
		}
		if stage.Replaces != "" {
			return errors.New("固定阶段不允许 replaces 或自动重试")
		}
		if len(stage.Resources) > 16 {
			return errors.New("固定阶段资源声明最多 16 项")
		}
		for _, resource := range stage.Resources {
			if strings.TrimSpace(resource) == "" || len(resource) > 200 {
				return errors.New("无效资源声明")
			}
		}
		total += len(stage.Task) + len(stage.Materials) + len(stage.Acceptance)
		steps = append(steps, PlanStep{ID: stage.ID, Title: stage.ID, Status: PlanPending, DependsOn: stage.DependsOn, Acceptance: stage.Acceptance})
	}
	if total > 1024*1024 {
		return errors.New("固定阶段材料总量超过 1 MiB")
	}
	if err := ValidatePlanSteps(steps); err != nil {
		return err
	}
	if e.Workflow != "fixed" {
		return nil
	}
	if !data.Delegation.Enabled || data.Delegation.MaxDepth < 1 || len(slots) == 0 {
		return errors.New("固定流程需要委派、至少一级深度与 worker 槽位，无需启用规划")
	}
	if e.Budget.AutoRetry {
		return errors.New("固定流程首版不支持自动重试")
	}
	if !data.AllowsTool("local__delegate_task") {
		return errors.New("固定流程需要公共委派工具能力")
	}
	if e.Main.RestrictTools {
		found := false
		for _, name := range e.Main.AllowedTools {
			if name == "local__delegate_task" {
				found = true
			}
		}
		if !found {
			return errors.New("固定流程需要 main 允许委派工具")
		}
	}
	b := e.Budget.Effective()
	calls := len(e.Stages) + 1
	if e.Review.Mode == "required" {
		calls++
	}
	if b.MaxAttempts < len(e.Stages) || b.MaxModelCalls < calls {
		return errors.New("固定流程预算不足以覆盖阶段、汇总与必要审查")
	}
	return nil
}

// Part of root creation: a fixed run cannot exist without its frozen stages.
// A crash never leaves a partially initialized plan or releases worker claims.
func initFixedGraph(ctx context.Context, tx *sql.Tx, root string, stages []TaskSpec) error {
	for i, spec := range stages {
		raw, err := json.Marshal(spec)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_items(root_run_id,id,position,spec,state) VALUES(?,?,?,?,'pending')`, root, spec.ID, i, string(raw)); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(stages)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO task_plan_revisions(root_run_id,revision,plan,created_at) VALUES(?,1,?,?)`, root, string(raw), now()); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE task_roots SET revision=1 WHERE run_id=?`, root)
	return err
}

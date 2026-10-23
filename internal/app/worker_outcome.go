package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"icetudio/internal/storage"
	"io"
	"strings"
)

const workerOutcomeContract = `本次为宿主调度任务，最终回答必须为 JSON 对象：{"status":"succeeded|blocked|failed","content":"具体交付文本或阻塞原因","evidenceCallIds":["本 Run 内已成功执行的模型工具调用ID"]}。正文可以使用用户要求的任何格式，但必须放在 content 中。不得把缺少工具、未执行检查或范围外工作声称为 succeeded。证据只能引用本次实际成功的工具调用；材料分析没有工具调用时可用空数组。宿主会验证结构和证据出处；succeeded 仅表示执行完成，不代表已通过独立审查。`

type workerOutcome struct {
	Status   string   `json:"status"`
	Content  string   `json:"content"`
	Evidence []string `json:"evidenceCallIds"`
}

func (s *Service) validateWorkerOutcome(ctx context.Context, run string, text string) (workerOutcome, error) {
	var o workerOutcome
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&o); err != nil {
		return o, fmt.Errorf("worker 未提交有效结果契约：%w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return o, errors.New("worker 结果契约后包含多余内容")
	}
	if strings.TrimSpace(o.Content) == "" || len(o.Content) > 128*1024 || len(o.Evidence) > 64 {
		return o, errors.New("worker 交付为空或超限")
	}
	switch o.Status {
	case "succeeded", "blocked", "failed":
	default:
		return o, errors.New("无效 worker 结果状态")
	}
	seen := map[string]bool{}
	for _, id := range o.Evidence {
		if seen[id] {
			return o, errors.New("重复工具证据")
		}
		seen[id] = true
		call, err := s.store.FindRunToolCall(ctx, run, id)
		if err != nil || call.State != storage.CallSucceeded {
			return o, errors.New("worker 引用了不属于本 Run 或未成功的证据")
		}
	}
	return o, nil
}

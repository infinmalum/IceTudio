import { ProfileStages } from "./ProfileStages";
import type { Configs } from "../hooks/useConfigs";
import {
  blankRootBudget,
  providerDefaults,
  providerLabels,
} from "../shared/config";
type Props = {
  configState: Pick<Configs, "configDraft" | "setConfigDraft">;
};
export function ProfileExecution(props: Props) {
  const { configDraft, setConfigDraft } = props.configState;
  if (!configDraft) return null;
  return (
    <>
      <details>
        <summary>执行组件与调度（开发者）</summary>
        <p className="hint">
          direct 按工具调用顺序执行；dynamic 由 planner 生成计划，宿主按依赖调度
          worker；fixed 按预设阶段执行，无需
          planner。独立审查可按需调用或设为交付门槛。
        </p>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={configDraft.data.execution.improverEnabled}
            onChange={(e) =>
              setConfigDraft({
                ...configDraft,
                data: {
                  ...configDraft.data,
                  execution: {
                    ...configDraft.data.execution,
                    improverEnabled: e.target.checked,
                  },
                },
              })
            }
          />
          允许显式生成配置改进建议
        </label>
        <label>
          执行流程
          <select
            value={configDraft.data.execution.workflow}
            onChange={(event) => {
              const dynamic = event.target.value === "dynamic";
              const fixed = event.target.value === "fixed";
              const scheduled = dynamic || fixed;
              const workers = configDraft.data.execution.workerSources ?? [];
              const worker = workers[0]?.name || "worker1";
              setConfigDraft({
                ...configDraft,
                data: {
                  ...configDraft.data,
                  planEnabled: fixed
                    ? false
                    : dynamic || configDraft.data.planEnabled,
                  delegation: {
                    ...configDraft.data.delegation,
                    enabled: scheduled || configDraft.data.delegation.enabled,
                    maxDepth: scheduled
                      ? Math.max(1, configDraft.data.delegation.maxDepth)
                      : configDraft.data.delegation.maxDepth,
                  },
                  execution: {
                    ...configDraft.data.execution,
                    workflow: event.target.value,
                    budget: {
                      ...configDraft.data.execution.budget,
                      autoRetry: fixed
                        ? false
                        : configDraft.data.execution.budget.autoRetry,
                    },
                    stages:
                      fixed &&
                      !(configDraft.data.execution.stages ?? []).length
                        ? [
                            {
                              id: "stage1",
                              worker,
                              task: "处理本次用户提供的材料",
                              accessMode: "read",
                              dependsOn: [],
                              acceptance: "给出具体结果",
                            },
                          ]
                        : configDraft.data.execution.stages,
                    workerSources:
                      scheduled && !workers.length
                        ? [
                            {
                              name: "worker1",
                              configuration: null,
                            },
                          ]
                        : configDraft.data.execution.workerSources,
                  },
                },
              });
            }}
          >
            <option value="direct">直接执行</option>
            <option value="dynamic">动态规划与协作</option>
            <option value="fixed">固定阶段流程（首版）</option>
          </select>
        </label>
        <p className="hint">
          动态流程由 planner 规划、宿主按依赖调度、main
          汇总。只读任务可以并行；写入和未知副作用任务串行。每次派发 worker
          仍需审批。
        </p>
        <div className="card-title">
          根任务预算（0 使用默认值；token 为 0 时仅统计）
        </div>
        <label className="checkbox-label">
          <input
            type="checkbox"
            disabled={configDraft.data.execution.workflow === "fixed"}
            checked={configDraft.data.execution.budget?.autoRetry ?? false}
            onChange={(event) =>
              setConfigDraft({
                ...configDraft,
                data: {
                  ...configDraft.data,
                  execution: {
                    ...configDraft.data.execution,
                    budget: {
                      ...(configDraft.data.execution.budget ??
                        blankRootBudget),
                      autoRetry: event.target.checked,
                    },
                  },
                },
              })
            }
          />
          允许无副作用失败任务的有界自动重试
        </label>
        {(
          [
            "maxSeconds",
            "maxModelCalls",
            "maxToolCalls",
            "maxTokens",
            "maxPlannerRounds",
            "maxAttempts",
            "maxRetries",
          ] as const
        ).map((key) => (
          <label key={key}>
            {
              {
                maxSeconds: "总秒数",
                maxModelCalls: "模型调用次数",
                maxToolCalls: "工具调用次数",
                maxTokens: "token 预算",
                maxPlannerRounds: "规划轮次",
                maxAttempts: "执行尝试次数",
                maxRetries: "每条链的重试次数",
              }[key]
            }
            <input
              type="number"
              min={0}
              value={configDraft.data.execution.budget?.[key] ?? 0}
              onChange={(event) =>
                setConfigDraft({
                  ...configDraft,
                  data: {
                    ...configDraft.data,
                    execution: {
                      ...configDraft.data.execution,
                      budget: {
                        ...(configDraft.data.execution.budget ??
                          blankRootBudget),
                        [key]: Number(event.target.value),
                      },
                    },
                  },
                })
              }
            />
          </label>
        ))}
        <label>
          独立审查
          <select
            value={configDraft.data.execution.review?.mode || "off"}
            onChange={(event) =>
              setConfigDraft({
                ...configDraft,
                data: {
                  ...configDraft.data,
                  execution: {
                    ...configDraft.data.execution,
                    review: {
                      ...(configDraft.data.execution.review ?? {
                        requiredChecks: [],
                      }),
                      mode: event.target.value,
                    },
                  },
                },
              })
            }
          >
            <option value="off">关闭</option>
            <option value="on_demand">按需（手动审查结果）</option>
            <option value="required">必需（未通过不能完成）</option>
          </select>
        </label>
        <label>
          必要检查工具（逗号分隔）
          <input
            value={(
              configDraft.data.execution.review?.requiredChecks ?? []
            ).join(",")}
            onChange={(event) =>
              setConfigDraft({
                ...configDraft,
                data: {
                  ...configDraft.data,
                  execution: {
                    ...configDraft.data.execution,
                    review: {
                      mode: configDraft.data.execution.review?.mode || "off",
                      requiredChecks: event.target.value
                        .split(",")
                        .map((x) => x.trim())
                        .filter(Boolean),
                    },
                  },
                },
              })
            }
            placeholder="例如 tests__run"
          />
        </label>
        <p className="hint">
          审查依据交付物和已有工具记录。必要检查需由执行组件完成并返回 JSON
          passed:true；未执行或结果未知时无法判定。直接与动态流程的必需审查最多返工两轮，共享任务预算；固定流程首版审查失败即停止，不自动改写阶段；按需审查只给出结论。
        </p>
        {(["main", "planner", "worker", "reviewer", "improver"] as const).map(
          (role) => {
            const component = configDraft.data.execution[role] ?? {
              systemPrompt: "",
              restrictTools: false,
              allowedTools: [],
              model: null,
            };
            const update = (patch: Partial<typeof component>) =>
              setConfigDraft({
                ...configDraft,
                data: {
                  ...configDraft.data,
                  execution: {
                    ...configDraft.data.execution,
                    [role]: { ...component, ...patch },
                  },
                },
              });
            return (
              <div key={role} className="model-form">
                <strong>
                  {role === "main"
                    ? "主执行"
                    : role === "planner"
                      ? "规划（仅生成计划）"
                      : role === "reviewer"
                        ? "独立审查（无工具）"
                        : role === "improver"
                          ? "配置改进（无工具，仅手动采用）"
                          : "子任务执行"}
                </strong>
                <label>
                  组件提示词（留空使用公共提示词）
                  <textarea
                    rows={3}
                    value={component.systemPrompt}
                    onChange={(event) =>
                      update({ systemPrompt: event.target.value })
                    }
                  />
                </label>
                <label>
                  组件模型
                  <select
                    value={component.model?.provider ?? ""}
                    onChange={(event) =>
                      update({
                        model: event.target.value
                          ? {
                              provider: event.target.value,
                              baseUrl:
                                providerDefaults[event.target.value] ?? "",
                              name: "",
                            }
                          : null,
                      })
                    }
                  >
                    <option value="">使用执行配置公共模型</option>
                    {Object.entries(providerLabels).map(([id, name]) => (
                      <option key={id} value={id}>
                        {name}
                      </option>
                    ))}
                  </select>
                </label>
                {component.model && (
                  <>
                    <label>
                      模型地址
                      <input
                        value={component.model.baseUrl}
                        onChange={(event) =>
                          update({
                            model: {
                              ...component.model!,
                              baseUrl: event.target.value,
                            },
                          })
                        }
                      />
                    </label>
                    <label>
                      模型 ID
                      <input
                        value={component.model.name}
                        onChange={(event) =>
                          update({
                            model: {
                              ...component.model!,
                              name: event.target.value,
                            },
                          })
                        }
                      />
                    </label>
                  </>
                )}
                {role !== "planner" &&
                  role !== "reviewer" &&
                  role !== "improver" && (
                    <>
                      <label className="checkbox-label">
                        <input
                          type="checkbox"
                          checked={component.restrictTools}
                          onChange={(event) =>
                            update({ restrictTools: event.target.checked })
                          }
                        />
                        限制为公共能力中的工具子集
                      </label>
                      {component.restrictTools && (
                        <label>
                          允许的工具（每行一个，空列表表示不允许工具）
                          <textarea
                            rows={2}
                            value={(component.allowedTools ?? []).join("\n")}
                            onChange={(event) =>
                              update({
                                allowedTools: event.target.value
                                  .split("\n")
                                  .map((name) => name.trim())
                                  .filter(Boolean),
                              })
                            }
                          />
                        </label>
                      )}
                    </>
                  )}
              </div>
            );
          },
        )}
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={configDraft.data.delegation.enabled}
            onChange={(event) =>
              setConfigDraft({
                ...configDraft,
                data: {
                  ...configDraft.data,
                  delegation: {
                    ...configDraft.data.delegation,
                    enabled: event.target.checked,
                    maxDepth: event.target.checked
                      ? Math.max(1, configDraft.data.delegation.maxDepth)
                      : configDraft.data.delegation.maxDepth,
                  },
                },
              })
            }
          />
          允许调用 worker（受对话授权与审批策略约束）
        </label>
        <div className="card-title">Worker 槽位</div>
        <p className="hint">
          任务开始时固定 worker 配置和 Skill
          内容；子任务只使用明确材料和此对话的权限，不读取历史或记忆。
        </p>
        {(configDraft.data.execution.workerSources ?? []).map(
          (source, index) => (
            <div key={index} className="model-form">
              <label>
                Worker 名称
                <input
                  value={source.name}
                  onChange={(event) => {
                    const sources = [
                      ...(configDraft.data.execution.workerSources ?? []),
                    ];
                    sources[index] = { ...source, name: event.target.value };
                    setConfigDraft({
                      ...configDraft,
                      data: {
                        ...configDraft.data,
                        execution: {
                          ...configDraft.data.execution,
                          workerSources: sources,
                        },
                      },
                    });
                  }}
                />
              </label>
              <button
                type="button"
                className="secondary"
                onClick={() =>
                  setConfigDraft({
                    ...configDraft,
                    data: {
                      ...configDraft.data,
                      execution: {
                        ...configDraft.data.execution,
                        workerSources: (
                          configDraft.data.execution.workerSources ?? []
                        ).filter((_, i) => i !== index),
                      },
                    },
                  })
                }
              >
                移除此 worker
              </button>
            </div>
          ),
        )}
        <button
          type="button"
          className="secondary"
          disabled={
            (configDraft.data.execution.workerSources ?? []).length >= 8
          }
          onClick={() => {
            const current = configDraft.data.execution.workerSources ?? [];
            let n = 1;
            while (current.some((source) => source.name === `worker${n}`)) n++;
            setConfigDraft({
              ...configDraft,
              data: {
                ...configDraft.data,
                execution: {
                  ...configDraft.data.execution,
                  workerSources: [
                    ...current,
                    { name: `worker${n}`, configuration: null },
                  ],
                },
              },
            });
          }}
        >
          ＋ 添加 worker
        </button>
        <p className="hint">
          direct 中委派按调用顺序同步执行；dynamic 由 planner 与宿主调度。worker
          槽位只提供配置，权限使用当前对话授权。
        </p>
        <ProfileStages configState={props.configState} />
      </details>
    </>
  );
}

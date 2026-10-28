import type { Configs } from "../hooks/useConfigs";
import { ErrorNotice } from "../shared/errors";
import { ProfileExecution } from "./ProfileExecution";
import { providerDefaults, remoteProviders } from "../shared/config";
export function ProfileAdvanced({ configState }: { configState: Configs }) {
  const { configDraft, setConfigDraft } = configState;
  return (
    <section className="session-card">
      <h2>助手的执行配置</h2>
      <p>
        与助手页编辑同一个内置助手，共用同一份草稿，保存后用于后续任务。
      </p>
      {configDraft && (
        <form className="model-form" onSubmit={configState.saveConfig}>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={configDraft.data.restrictTools ?? false}
              onChange={(event) =>
                setConfigDraft({
                  ...configDraft,
                  data: { ...configDraft.data, restrictTools: event.target.checked },
                })
              }
            />
            将空工具清单视为禁用全部工具
          </label>
          <label>
            工具允许清单（每行一个；非空时始终限制工具，含原生工程工具）
            <textarea
              rows={3}
              value={(configDraft.data.allowedTools ?? []).join("\n")}
              onChange={(event) =>
                setConfigDraft({
                  ...configDraft,
                  data: {
                    ...configDraft.data,
                    allowedTools: event.target.value
                      .split("\n")
                      .map((name) => name.trim())
                      .filter(Boolean),
                  },
                })
              }
            />
          </label>
          <p className="hint">清单留空且未勾选时，使用原生工程工具与已启用服务的工具；勾选后留空则禁用全部工具。</p>
          <label>
            模型来源
            <select
              value={configDraft.data.model.provider || "default"}
              onChange={(event) =>
                setConfigDraft({
                  ...configDraft,
                  data: {
                    ...configDraft.data,
                    model:
                      event.target.value === "default"
                        ? { provider: "", baseUrl: "", name: "" }
                        : {
                            provider: event.target.value,
                            baseUrl: providerDefaults[event.target.value] ?? "",
                            name: "",
                          },
                  },
                })
              }
            >
              <option value="default">使用应用默认模型</option>
              <option value="ollama">Ollama</option>
              <option value="lmstudio">LM Studio</option>
              <option value="openai">OpenAI</option>
              <option value="anthropic">Anthropic</option>
              <option value="gemini">Gemini</option>
            </select>
          </label>
          {configDraft.data.model.provider && (
            <>
              <label>
                这个助手的模型地址
                {remoteProviders.includes(
                  configDraft.data.model
                    .provider as (typeof remoteProviders)[number],
                )
                  ? "（留空使用官方端点）"
                  : ""}
                <input
                  value={configDraft.data.model.baseUrl}
                  onChange={(event) =>
                    setConfigDraft({
                      ...configDraft,
                      data: {
                        ...configDraft.data,
                        model: {
                          ...configDraft.data.model,
                          baseUrl: event.target.value,
                        },
                      },
                    })
                  }
                  placeholder={
                    providerDefaults[configDraft.data.model.provider] ?? ""
                  }
                />
              </label>
              <label>
                这个助手的模型名称
                <input
                  value={configDraft.data.model.name}
                  onChange={(event) =>
                    setConfigDraft({
                      ...configDraft,
                      data: {
                        ...configDraft.data,
                        model: {
                          ...configDraft.data.model,
                          name: event.target.value,
                        },
                      },
                    })
                  }
                />
              </label>
            </>
          )}
          <label>
            人格
            <textarea
              value={configDraft.data.persona}
              onChange={(event) =>
                setConfigDraft({
                  ...configDraft,
                  data: { ...configDraft.data, persona: event.target.value },
                })
              }
            />
          </label>
          <label>
            上下文预算（token）
            <input
              type="number"
              min={1024}
              max={1000000}
              value={configDraft.data.contextBudget}
              onChange={(event) =>
                setConfigDraft({
                  ...configDraft,
                  data: {
                    ...configDraft.data,
                    contextBudget: Number(event.target.value),
                  },
                })
              }
            />
          </label>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={configDraft.data.planEnabled}
              onChange={(event) =>
                setConfigDraft({
                  ...configDraft,
                  data: {
                    ...configDraft.data,
                    planEnabled: event.target.checked,
                  },
                })
              }
            />
            启用任务规划（会话待办；配置 worker 后可声明本次执行计划）
          </label>
          <ProfileExecution configState={configState} />

          <button disabled={configState.configBusy}>保存执行配置</button>
          {configState.dirty && (
            <p role="status">
              有未保存的助手修改，保存会包含这份草稿中的全部修改。
            </p>
          )}
        </form>
      )}
      <ErrorNotice error={configState.configError} />
    </section>
  );
}

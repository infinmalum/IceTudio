import { ErrorNotice } from "../shared/errors";
import type { Configs } from "../hooks/useConfigs";
import type { MCPServer } from "../../bindings/icetudio/internal/config";
import type { SkillInfo } from "../../bindings/icetudio/internal/app";
import { ProfileCapabilities } from "./ProfileCapabilities";
type Props = {
  openDeveloper: () => void;
  openExtensions: () => void;
  skillCatalog: SkillInfo[];
  mcpCatalog: MCPServer[];
  configState: Pick<
    Configs,
    | "configBusy"
    | "configDraft"
    | "configError"
    | "restoreDefault"
    | "saveConfig"
    | "setConfigDraft"
  >;
};
// The product has exactly one creation assistant (the built-in default
// RuntimeConfig); this page edits it. No config library, no create/copy.
export function ProfileEditor(props: Props) {
  const { configBusy, configDraft, configError, restoreDefault, saveConfig, setConfigDraft } =
    props.configState;
  return (
    configDraft && (
      <section className="session-card" id="assistant-editor">
        <div className="card-title">
          助手「{configDraft.name}」{" "}
          <span className="revision" hidden>
            v{configDraft.revision}
          </span>
        </div>
        {configDraft.isDefault && (
          <div className="model-actions">
            <button
              type="button"
              className="secondary"
              disabled={configBusy}
              onClick={() => void restoreDefault()}
            >
              恢复默认配置
            </button>
          </div>
        )}
        <form className="model-form" onSubmit={saveConfig}>
          <label>
            助手名称
            <input
              value={configDraft.name}
              onChange={(event) =>
                setConfigDraft({ ...configDraft, name: event.target.value })
              }
              required
            />
          </label>
          <label>
            说明
            <input
              value={configDraft.data.description}
              onChange={(event) =>
                setConfigDraft({
                  ...configDraft,
                  data: {
                    ...configDraft.data,
                    description: event.target.value,
                  },
                })
              }
            />
          </label>
          <label>
            希望助手怎么做
            <textarea
              rows={4}
              value={configDraft.data.systemPrompt}
              onChange={(event) =>
                setConfigDraft({
                  ...configDraft,
                  data: {
                    ...configDraft.data,
                    systemPrompt: event.target.value,
                  },
                })
              }
            />
          </label>
          <ProfileCapabilities
            configState={props.configState}
            skills={props.skillCatalog}
            mcpServers={props.mcpCatalog}
            openExtensions={props.openExtensions}
          />
          <button
            type="button"
            className="secondary"
            onClick={props.openDeveloper}
          >
            配置模型覆盖与工作流程 →
          </button>

          <div className="model-actions">
            <button type="submit" disabled={configBusy}>
              保存助手
            </button>
          </div>
        </form>
        <ErrorNotice error={configError} />
        <p className="hint">
          保存后用于后续工作。导出助手不包含聊天记录、权限或密钥。
        </p>
      </section>
    )
  );
}

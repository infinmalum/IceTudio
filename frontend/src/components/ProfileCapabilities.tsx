import type { MCPServer } from "../../bindings/icetudio/internal/config";
import type { SkillInfo } from "../../bindings/icetudio/internal/app";
import type { Configs } from "../hooks/useConfigs";
function toggle(list: string[], name: string) {
  return list.includes(name)
    ? list.filter((item) => item !== name)
    : [...list, name];
}
export function ProfileCapabilities({
  configState,
  skills,
  mcpServers,
  openExtensions,
}: {
  configState: Pick<Configs, "configDraft" | "setConfigDraft">;
  skills: SkillInfo[];
  mcpServers: MCPServer[];
  openExtensions: () => void;
}) {
  const { configDraft, setConfigDraft } = configState;
  if (!configDraft) return null;
  const enabledMcp = configDraft.data.enabledMcpServers ?? [];
  const enabledSkills = configDraft.data.enabledSkills ?? [];
  const missingMcp = enabledMcp.filter(
    (name) => !mcpServers.some((server) => server.name === name),
  );
  const missingSkills = enabledSkills.filter(
    (name) => !skills.some((item) => item.name === name),
  );
  return (
    <section className="assistant-capabilities" aria-label="助手的技能与扩展">
      <h3>技能与扩展</h3>
      <p className="hint">
        勾选这个助手可以使用的已安装 Skill 和已接入 MCP
        服务。想逐个挑选时，取消勾选"启用全部"，下面的列表就会出现。这里只选择已有能力；接入新的
        MCP 服务在{" "}
        <button type="button" className="text-button" onClick={openExtensions}>
          设置 → 技能与服务
        </button>{" "}
        中进行。
      </p>
      <label className="checkbox-label">
        <input
          type="checkbox"
          checked={configDraft.data.allMcpServers}
          onChange={(event) =>
            setConfigDraft({
              ...configDraft,
              data: {
                ...configDraft.data,
                allMcpServers: event.target.checked,
              },
            })
          }
        />
        启用全部已接入的 MCP 服务
      </label>
      {!configDraft.data.allMcpServers && (
        <div className="capability-list">
          {mcpServers.map((server) => (
            <label className="checkbox-label" key={server.name}>
              <input
                type="checkbox"
                checked={enabledMcp.includes(server.name)}
                onChange={() =>
                  setConfigDraft({
                    ...configDraft,
                    data: {
                      ...configDraft.data,
                      enabledMcpServers: toggle(enabledMcp, server.name),
                    },
                  })
                }
              />
              {server.name}
              <span className="memory-meta"> {server.command}</span>
            </label>
          ))}
          {missingMcp.map((name) => (
            <label className="checkbox-label" key={name}>
              <input
                type="checkbox"
                checked
                onChange={() =>
                  setConfigDraft({
                    ...configDraft,
                    data: {
                      ...configDraft.data,
                      enabledMcpServers: toggle(enabledMcp, name),
                    },
                  })
                }
              />
              {name}
              <span className="memory-meta sensitive">（未接入，保存前请取消勾选或先接入）</span>
            </label>
          ))}
          {mcpServers.length === 0 && missingMcp.length === 0 && (
            <p className="hint">尚未接入任何 MCP 服务。</p>
          )}
        </div>
      )}
      <label className="checkbox-label">
        <input
          type="checkbox"
          checked={configDraft.data.allSkills}
          onChange={(event) =>
            setConfigDraft({
              ...configDraft,
              data: {
                ...configDraft.data,
                allSkills: event.target.checked,
              },
            })
          }
        />
        启用全部已安装的 Skills
      </label>
      {!configDraft.data.allSkills && (
        <div className="capability-list">
          {skills.map((skill) => (
            <label className="checkbox-label" key={skill.name}>
              <input
                type="checkbox"
                checked={enabledSkills.includes(skill.name)}
                onChange={() =>
                  setConfigDraft({
                    ...configDraft,
                    data: {
                      ...configDraft.data,
                      enabledSkills: toggle(enabledSkills, skill.name),
                    },
                  })
                }
              />
              {skill.name}
              {skill.managed && (
                <span className="memory-meta">（AI 生成）</span>
              )}
              <span className="memory-meta"> {skill.description}</span>
            </label>
          ))}
          {missingSkills.map((name) => (
            <label className="checkbox-label" key={name}>
              <input
                type="checkbox"
                checked
                onChange={() =>
                  setConfigDraft({
                    ...configDraft,
                    data: {
                      ...configDraft.data,
                      enabledSkills: toggle(enabledSkills, name),
                    },
                  })
                }
              />
              {name}
              <span className="memory-meta sensitive">（未安装，保存前请取消勾选或先安装）</span>
            </label>
          ))}
          {skills.length === 0 && missingSkills.length === 0 && (
            <p className="hint">本机还没有已安装的 Skills。</p>
          )}
        </div>
      )}
    </section>
  );
}

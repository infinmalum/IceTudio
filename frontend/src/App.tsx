import { useState } from "react";
import { useApp } from "./hooks/useApp";
import { Sidebar } from "./components/Sidebar";
import { ProjectWorkspace } from "./components/ProjectWorkspace";
import { ConversationPanel } from "./components/ConversationPanel";
import { loadPaneWidth, paneStyle, PaneResizer } from "./shared/panes";
import { ProfileAdvanced } from "./components/ProfileAdvanced";
import { ProfileEditor } from "./components/ProfileEditor";
import { ModelSettings } from "./components/ModelSettings";
import { PrivacySettings } from "./components/PrivacySettings";
import { ContextPolicyPanel } from "./components/ContextPolicyPanel";
import { ExfilPanel } from "./components/ExfilPanel";
import { MCPServersPanel } from "./components/MCPServersPanel";
import { HostActivityPanel } from "./components/HostActivityPanel";
import { SkillDrafts } from "./components/SkillDrafts";
import { ErrorNotice } from "./shared/errors";
import { StatusService } from "../bindings/icetudio";

// Column widths survive restarts; null keeps the CSS defaults, including the
// automatic narrower widths below 1080px.
function usePaneWidth(key: string) {
  const [width, setWidth] = useState(() => loadPaneWidth(key));
  const set = (value: number | null) => {
    try {
      if (value == null) localStorage.removeItem(key);
      else localStorage.setItem(key, `${value}px`);
    } catch {
      // Storage unavailable: keep the width for this session only.
    }
    setWidth(value == null ? null : `${value}px`);
  };
  return [width, set] as const;
}

function App() {
  const app = useApp();
  const w = app.workspace;
  const [sidebarWidth, setSidebarWidth] = usePaneWidth(
    "icetudio.sidebar-width",
  );
  const [dockWidth, setDockWidth] = usePaneWidth("icetudio.dock-width");
  const openModel = () => {
    app.setSettingsTab("model");
    app.setPage("settings");
  };
  const tasks = app.conversation.runs
    .filter(
      (r) =>
        r.component === "main" &&
        !r.parentRunId &&
        r.status !== "running" &&
        app.conversation.messages.some(
          (m) => m.runId === r.id && m.role === "user",
        ),
    )
    .map((r) => ({
      id: r.id,
      label:
        (app.conversation.messages
          .find((m) => m.runId === r.id && m.role === "user")
          ?.content.slice(0, 64) || "最近的工作") +
        " · " +
        new Date(r.createdAt).toLocaleString(),
    }));
  async function useMethod(name: string) {
    const config = app.configState.configDraft;
    if (!config) return;
    if (app.configState.dirty) {
      await app.configState.reportError("请先保存助手修改，再添加工作方法。");
      return;
    }
    try {
      await StatusService.UpdateRuntimeConfig({
        ...config,
        data: {
          ...config.data,
          enabledSkills: [
            ...new Set([...(config.data.enabledSkills ?? []), name]),
          ],
        },
      });
      await app.configState.refreshConfigs();
    } catch (e) {
      await app.configState.reportError(e);
    }
  }
  return (
    <div className="shell">
      <Sidebar
        projects={app.projects}
        page={app.page}
        setPage={app.setPage}
        style={paneStyle(sidebarWidth)}
      />
      <PaneResizer
        label="调整工程栏宽度"
        targetSelector=".sidebar"
        grow={1}
        min={180}
        max={420}
        onWidth={setSidebarWidth}
      />
      <main className="content">
        {app.page === "workspace" ? (
          <div className="workspace-page">
            <section className="project-stage">
              <ErrorNotice
                error={
                  app.runtime.status && !app.runtime.status.storageReady
                    ? app.runtime.status.message
                    : app.error
                }
              />
              <ProjectWorkspace projects={app.projects} />
            </section>
            <PaneResizer
              className="dock-resizer"
              label="调整对话栏宽度"
              targetSelector=".agent-dock"
              grow={-1}
              min={320}
              max={640}
              onWidth={setDockWidth}
            />
            <aside
              className="agent-dock"
              style={paneStyle(dockWidth)}
            >
              <ConversationPanel
                workspace={w}
                conversation={app.conversation}
                configState={app.configState}
                runtime={app.runtime}
                projects={app.projects}
                openModel={openModel}
                openAssistants={() => app.setPage("assistants")}
              />
            </aside>
          </div>
        ) : (
          <div className="settings-page">
            <header className="page-header">
              <div>
                <p className="eyebrow">IceTudio</p>
                <h1>{app.page === "assistants" ? "创作助手" : "设置"}</h1>
              </div>
              <button
                className="secondary"
                onClick={() => app.setPage("workspace")}
              >
                返回工程
              </button>
            </header>
            <ErrorNotice
              error={
                app.runtime.status && !app.runtime.status.storageReady
                  ? app.runtime.status.message
                  : app.error
              }
            />
            {app.page === "assistants" ? (
              <>
                <ProfileEditor
                  configState={app.configState}
                  skillCatalog={app.skillCatalog}
                  mcpCatalog={app.mcpCatalog}
                  openDeveloper={() => {
                    app.setSettingsTab("developer");
                    app.setPage("settings");
                  }}
                  openExtensions={() => {
                    app.setSettingsTab("extensions");
                    app.setPage("settings");
                  }}
                />
                {app.configState.dirty && (
                  <p className="unsaved-note" role="status">
                    有未保存的助手修改，切换页面时会保留草稿。
                  </p>
                )}
                <SkillDrafts
                  state={app.skillDrafts}
                  hasConversation={!!w.selected}
                  tasks={tasks}
                  useMethod={useMethod}
                  skillNames={app.skillCatalog.map((item) => item.name)}
                />
              </>
            ) : (
              <>
                <nav className="settings-tabs" aria-label="设置分类">
                  {[
                    ["model", "模型连接"],
                    ["extensions", "技能与服务"],
                    ["privacy", "权限"],
                    ["developer", "开发者设置"],
                  ].map(([id, label]) => (
                    <button
                      className={
                        app.settingsTab === id ? "selected" : "secondary"
                      }
                      key={id}
                      onClick={() => app.setSettingsTab(id)}
                    >
                      {label}
                    </button>
                  ))}
                </nav>
                {app.settingsTab === "model" ? (
                  <ModelSettings
                    runtime={app.runtime}
                    onConnected={() => app.setPage("workspace")}
                  />
                ) : app.settingsTab === "extensions" ? (
                  <>
                    <section className="session-card">
                      <h2>已安装的 Skills</h2>
                      {app.skillCatalog.length ? (
                        <div className="capability-list">
                          {app.skillCatalog.map((skill) => (
                            <div className="checkbox-label" key={skill.name}>
                              {skill.name}
                              {skill.managed && (
                                <span className="memory-meta">（AI 生成）</span>
                              )}
                              <span className="memory-meta">
                                {skill.description}
                              </span>
                            </div>
                          ))}
                        </div>
                      ) : (
                        <p className="hint">
                          本机还没有已安装的 Skills。AI
                          可以在对话中起草并发布新的 Skill。
                        </p>
                      )}
                      <p className="hint">
                        每个助手能用哪些 Skills
                        和服务，在助手编辑页的「技能与扩展」里勾选。
                      </p>
                    </section>
                    <MCPServersPanel
                      servers={app.mcpCatalog}
                      runtime={app.runtime}
                      onChanged={app.refreshCapabilities}
                    />
                    {app.catalogError && (
                      <ErrorNotice error={app.catalogError} />
                    )}
                  </>
                ) : app.settingsTab === "privacy" ? (
                  <>
                    <PrivacySettings
                      local={app.local}
                      workspace={w}
                      searchAvailability={app.conversation.searchAvailability}
                    />
                  </>
                ) : (
                  <>
                    <p>集中管理助手的执行配置与本机高级选项。</p>
                    <ProfileAdvanced configState={app.configState} />
                    <button
                      className="secondary"
                      onClick={() => app.setPage("assistants")}
                    >
                      返回助手编辑
                    </button>
                    <HostActivityPanel activity={app.activity} />
                    <details>
                      <summary>本机权限的完整配置</summary>
                      <ContextPolicyPanel local={app.local} workspace={w} />
                    </details>
                    <details>
                      <summary>内容来源与目的地授权</summary>
                      <ExfilPanel local={app.local} workspace={w} />
                    </details>
                    <label className="checkbox-label">
                      <input
                        type="checkbox"
                        checked={w.reuseContext}
                        onChange={(e) => w.setReuseContext(e.target.checked)}
                      />
                      下一个新对话沿用当前记忆与权限
                    </label>
                  </>
                )}
              </>
            )}
          </div>
        )}
      </main>
    </div>
  );
}
export default App;

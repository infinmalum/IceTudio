import { useSkillDrafts } from "./useSkillDrafts";
import { useEffect, useState } from "react";
import { Events } from "@wailsio/runtime";
import { StatusService } from "../../bindings/icetudio";
import type { SkillInfo } from "../../bindings/icetudio/internal/app";
import type { MCPServer } from "../../bindings/icetudio/internal/config";
import { useWorkspace } from "./useWorkspace";
import { useProjects } from "./useProjects";
import { useRuntimeSettings } from "./useRuntimeSettings";
import { useConfigs } from "./useConfigs";
import { useLocalContext } from "./useLocalContext";
import { useConversation } from "./useConversation";
export type ActivityEntry = {
  at: number;
  type: string;
  sessionId: string;
  runId: string;
  detail: string;
};
export function useApp() {
  const workspace = useWorkspace();
  const runtime = useRuntimeSettings();
  const configs = useConfigs();
  const local = useLocalContext(workspace.contextID, workspace.selected);
  const conversation = useConversation(workspace, runtime.status);
  // The music project strip: stored projects plus the shared edit service
  // (rename/undo/redo), bound to the active conversation's project.
  const projects = useProjects(
    workspace.activeConversation?.id ?? null,
    workspace.activeConversation?.projectId ?? "",
    workspace.refreshConversations,
  );
  const skillDrafts = useSkillDrafts(workspace.selected);
  const [page, setPage] = useState<"workspace" | "assistants" | "settings">(
    "workspace",
  );
  const [settingsTab, setSettingsTab] = useState("model");
  // The installed-capability catalogs: assistant editing picks from these
  // instead of typing names, and the MCP panel edits the same source.
  const [skillCatalog, setSkillCatalog] = useState<SkillInfo[]>([]);
  const [mcpCatalog, setMcpCatalog] = useState<MCPServer[]>([]);
  const [catalogError, setCatalogError] = useState("");
  async function refreshCapabilities() {
    try {
      const [skills, servers] = await Promise.all([
        StatusService.ListSkills(),
        StatusService.ListConfiguredMCPServers(),
      ]);
      setSkillCatalog(skills ?? []);
      setMcpCatalog(servers ?? []);
      setCatalogError("");
    } catch (reason) {
      setCatalogError(String(reason));
    }
  }
  useEffect(() => {
    void refreshCapabilities();
  }, []);
  // Every post-commit host event reaches the webview already; keep a rolling
  // activity view so "what is the host doing" is visible without the CLI.
  const [activity, setActivity] = useState<ActivityEntry[]>([]);
  useEffect(() => {
    const off = Events.On("icetudio:service-event", (event) => {
      const detail =
        (event.data as {
          type?: string;
          sessionId?: string;
          runId?: string;
          detail?: string;
        }) ?? {};
      setActivity((current) =>
        [
          {
            at: Date.now(),
            type: detail.type ?? "unknown",
            sessionId: detail.sessionId ?? "",
            runId: detail.runId ?? "",
            detail: detail.detail ?? "",
          },
          ...current,
        ].slice(0, 200),
      );
    });
    return () => off();
  }, []);
  return {
    workspace,
    skillDrafts,
    runtime,
    configState: configs,
    local,
    conversation,
    projects,
    error: runtime.error || workspace.workspaceError,
    page,
    setPage,
    settingsTab,
    setSettingsTab,
    skillCatalog,
    mcpCatalog,
    catalogError,
    refreshCapabilities,
    activity,
  };
}

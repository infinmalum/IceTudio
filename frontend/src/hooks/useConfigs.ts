import { useEffect, useRef, useState } from "react";
import { StatusService } from "../../bindings/icetudio";
import type { RuntimeConfig } from "../../bindings/icetudio/internal/storage";
import { useDialog } from "../shared/dialogs";
export function useConfigs() {
  const dialog = useDialog();
  // Unsaved edits per config survive switching between editors in one visit;
  // they are local draft state only and never touch the backend revision.
  const cached = useRef(new Map<string, RuntimeConfig>());
  const [configs, setConfigs] = useState<RuntimeConfig[]>([]);
  const [configEditID, chooseID] = useState<string | null>(null);
  const [configDraft, setDraft] = useState<RuntimeConfig | null>(null);
  const [configBusy, setConfigBusy] = useState(false);
  const [configError, setConfigError] = useState("");
  const creating = useRef(false);
  useEffect(() => {
    const target = configs.find((item) => item.id === configEditID) ?? null;
    setDraft(
      target ? structuredClone(cached.current.get(target.id) ?? target) : null,
    );
  }, [configEditID, configs]);
  const dirty =
    !!configDraft &&
    JSON.stringify(configDraft) !==
      JSON.stringify(configs.find((c) => c.id === configDraft.id));
  function setConfigDraft(value: RuntimeConfig | null) {
    if (value) {
      if (
        JSON.stringify(value) ===
        JSON.stringify(configs.find((c) => c.id === value.id))
      )
        cached.current.delete(value.id);
      else cached.current.set(value.id, structuredClone(value));
    }
    setDraft(value);
  }
  async function setConfigEditID(id: string | null) {
    if (id === configEditID) return true;
    if (
      dirty &&
      !(await dialog.confirm({
        title: "保留未保存的修改？",
        body: "当前助手的修改会留在本机草稿中，稍后切回来可继续编辑。",
        confirm: "保留并切换",
      }))
    )
      return false;
    chooseID(id);
    return true;
  }
  async function refreshConfigs() {
    const items = await StatusService.ListRuntimeConfigs();
    setConfigs(items ?? []);
  }
  async function saveConfig(event?: React.FormEvent<HTMLFormElement>) {
    event?.preventDefault();
    if (!configDraft) return;
    setConfigBusy(true);
    try {
      const saved = await StatusService.UpdateRuntimeConfig(configDraft);
      cached.current.delete(saved.id);
      setConfigError("");
      await refreshConfigs();
      setDraft(saved);
    } catch (reason) {
      setConfigError(String(reason));
    } finally {
      setConfigBusy(false);
    }
  }
  async function createConfig(name = "", fromID = "") {
    if (creating.current || configBusy) return null;
    creating.current = true;
    setConfigBusy(true);
    try {
      if (!fromID && !name.trim()) {
        name =
          (await dialog.ask({
            title: "创建助手",
            label: "助手名称",
            confirm: "创建",
            singleLine: true,
          })) ?? "";
        if (!name.trim()) return null;
      }
      const created = await StatusService.CreateRuntimeConfig(
        name.trim(),
        fromID,
      );
      setConfigs((current) => [
        ...current.filter((c) => c.id !== created.id),
        created,
      ]);
      chooseID(created.id);
      setConfigError("");
      return created;
    } catch (reason) {
      setConfigError(String(reason));
      return null;
    } finally {
      creating.current = false;
      setConfigBusy(false);
    }
  }
  async function restoreDefault() {
    if (!configDraft) return;
    if (!configDraft.isDefault) {
      setConfigError("只能恢复内置的默认执行配置。");
      return;
    }
    if (
      !(await dialog.confirm({
        title: "恢复默认执行配置？",
        body: "这会替换内置默认配置的内容和未保存的修改。已进行的工作会保留。",
        confirm: "恢复默认",
        danger: true,
      }))
    )
      return;
    setConfigBusy(true);
    try {
      const restored = await StatusService.RestoreRuntimeConfigDefault(
        configDraft.revision,
      );
      await refreshConfigs();
      cached.current.delete(restored.id);
      setDraft(restored);
      setConfigError("");
    } catch (reason) {
      setConfigError(String(reason));
    } finally {
      setConfigBusy(false);
    }
  }
  useEffect(() => {
    StatusService.ListRuntimeConfigs()
      .then((items) => {
        setConfigs(items ?? []);
        chooseID(
          (items ?? []).find((item) => item.isDefault)?.id ??
            items?.[0]?.id ??
            null,
        );
      })
      .catch((reason) => setConfigError(String(reason)));
  }, []);
  return {
    configs,
    configEditID,
    setConfigEditID,
    configDraft,
    setConfigDraft,
    dirty,
    configBusy,
    configError,
    reportError: async (reason: unknown) => setConfigError(String(reason)),
    refreshConfigs,
    saveConfig,
    createConfig,
    restoreDefault,
  };
}
export type Configs = ReturnType<typeof useConfigs>;

import { useEffect, useRef, useState } from "react";
import { StatusService, type AppStatus } from "../../bindings/icetudio";
import type {
  CredentialStatus,
  MCPEndpointStatus,
} from "../../bindings/icetudio/internal/app";
import { useDialog } from "../shared/dialogs";
import { type ModelDraft } from "../shared/config";
export function useRuntimeSettings() {
  const dialog = useDialog();
  const [status, setStatus] = useState<AppStatus | null>(null);
  const [error, setError] = useState("");
  const [mcpServers, setMcpServers] = useState<MCPEndpointStatus[]>([]);
  const modelEdited = useRef(false);
  const [modelDraft, setModelDraftState] = useState<ModelDraft>({
    provider: "ollama",
    baseUrl: "http://127.0.0.1:11434/v1",
    name: "",
  });
  function setModelDraft(value: ModelDraft) {
    modelEdited.current = true;
    setModelDraftState(value);
    setConnectedMessage("");
  }
  const [modelBusy, setModelBusy] = useState(false);
  const [modelError, setModelError] = useState("");
  const [credStatus, setCredStatus] = useState<CredentialStatus[]>([]);
  const [apiKey, setApiKey] = useState("");
  const [connectedMessage, setConnectedMessage] = useState("");
  const [credBusy, setCredBusy] = useState(false);
  const [credError, setCredError] = useState("");
  async function refreshModel() {
    setModelBusy(true);
    try {
      const value = await StatusService.GetStatus();
      setStatus(value);
      setError("");
      setModelError(value.modelConnected ? "" : value.message);
      setConnectedMessage(value.modelConnected ? "已保存的模型连接正常。" : "");
    } catch (reason) {
      setModelError(String(reason));
    } finally {
      setModelBusy(false);
    }
  }
  async function saveModel(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setModelBusy(true);
    setConnectedMessage("");
    try {
      if (apiKey.trim())
        await StatusService.SaveCredential(modelDraft.provider, apiKey.trim());
      setApiKey("");
      const value = await StatusService.SaveModelConfig(modelDraft);
      setStatus(value);
      setError("");
      setCredStatus((await StatusService.ListCredentialStatus()) ?? []);
      if (!value.modelConnected) {
        setModelError(value.message);
        return;
      }
      setModelError("");
      setConnectedMessage(
        value.model.name
          ? "模型已连接，可以开始聊天。"
          : "服务已连接，请选择一个模型后保存。",
      );
    } catch (e) {
      setModelError(String(e));
    } finally {
      setModelBusy(false);
    }
  }
  async function deleteCredential(provider: string) {
    if (
      !(await dialog.confirm({
        title: "删除保存的密钥？",
        body: "使用此服务的助手将需要重新填写密钥。聊天记录不会删除。",
        confirm: "删除密钥",
        danger: true,
      }))
    )
      return;
    setCredBusy(true);
    try {
      await StatusService.DeleteCredential(provider);
      setCredStatus((await StatusService.ListCredentialStatus()) ?? []);
      setStatus(await StatusService.GetStatus());
      setConnectedMessage("");
      setCredError("");
    } catch (reason) {
      setCredError(String(reason));
    } finally {
      setCredBusy(false);
    }
  }
  async function refreshEndpoints() {
    try {
      const items = await StatusService.ListMCPEndpoints();
      setMcpServers(items ?? []);
    } catch (reason) {
      setError(String(reason));
    }
  }
  useEffect(() => {
    StatusService.GetStatus()
      .then((value) => {
        setStatus(value);
        setError("");
        if (!modelEdited.current) setModelDraftState(value.model);
      })
      .catch((reason) => setError(String(reason)));
    StatusService.ListMCPEndpoints()
      .then((items) => setMcpServers(items ?? []))
      .catch((reason) => setError(String(reason)));
    StatusService.ListCredentialStatus()
      .then((items) => setCredStatus(items ?? []))
      .catch((reason) => setCredError(String(reason)));
  }, []);
  return {
    status,
    error,
    mcpServers,
    refreshEndpoints,
    modelDraft,
    setModelDraft,
    modelBusy,
    modelError,
    credStatus,
    credBusy,
    credError,
    refreshModel,
    saveModel,
    apiKey,
    setApiKey,
    connectedMessage,
    deleteCredential,
  };
}
export type RuntimeSettings = ReturnType<typeof useRuntimeSettings>;

import { StatusService } from "../../bindings/icetudio";
import { scopeLabels, providerLabels } from "./config";
import { useDialog } from "./dialogs";
export function useSendConsent() {
  const dialog = useDialog();
  return async (session: string, attachments = false, role = "main") => {
    const preview = await StatusService.PreviewSend(session, attachments, role);
    for (const item of preview ?? []) {
      if (!(item.missing ?? []).length) continue;
      const allowed = await dialog.confirm({
        title: `允许将内容发送给 ${providerLabels[item.provider] ?? "模型服务"}？`,
        body: (
          <>
            <p>
              实际接收地址：<strong>{item.destination}</strong>
            </p>
            <p>
              本对话会发送：
              {(item.scopes ?? []).map((v) => scopeLabels[v] ?? v).join("、")}
              。包括本次输入，以及此助手获准使用的相关资料和处理结果。
            </p>
            {(item.scopes ?? []).includes("credential") && (
              <p className="error">
                其中包含被标记为凭据的敏感内容。请确认该服务可信，或改用本机模型。
              </p>
            )}
            <p>
              授权用于这个对话；只有你主动设置共享权限的对话会一同使用它。可随时在设置中撤销，读取文件等操作仍需单独允许。
            </p>
          </>
        ),
        confirm: "允许并继续",
        cancel: "暂不发送",
      });
      if (!allowed) return false;
      const conversation = await StatusService.GetConversation(session);
      const grants = await StatusService.ListExfilGrants(
        conversation.contextId,
      );
      const existing =
        grants?.find((g) => g.destination === item.destination)?.scopes ?? [];
      await StatusService.GrantExfil(conversation.contextId, item.destination, [
        ...new Set([...existing, ...(item.scopes ?? [])]),
      ]);
    }
    return true;
  };
}

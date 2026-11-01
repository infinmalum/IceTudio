import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { DialogProvider } from "./shared/dialogs";
import { CloseGuardProvider } from "./shared/closeGuard";
import { applyUIScale, loadUIScale } from "./shared/uiScale";

// 在首次渲染前恢复界面缩放，避免启动后二次跳变。
applyUIScale(loadUIScale());

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <DialogProvider>
      <CloseGuardProvider><App /></CloseGuardProvider>
    </DialogProvider>
  </React.StrictMode>,
);

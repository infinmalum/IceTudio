import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { DialogProvider } from "./shared/dialogs";
import { CloseGuardProvider } from "./shared/closeGuard";

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <DialogProvider>
      <CloseGuardProvider><App /></CloseGuardProvider>
    </DialogProvider>
  </React.StrictMode>,
);

// Isolated test entry; uses the production workspace and hooks with an
// authenticated bridge to the real Go service supplied by the Go test.
import React from "react";
import { createRoot } from "react-dom/client";
import { DialogProvider } from "../src/shared/dialogs";
import { useProjects } from "../src/hooks/useProjects";
import { ProjectWorkspace } from "../src/components/ProjectWorkspace";
import { CloseGuardProvider } from "../src/shared/closeGuard";
function MusicTest() {
  const projects = useProjects(null, "");
  return <div style={{ padding: 20, height: "100vh", overflow: "auto" }}><ProjectWorkspace projects={projects} openModel={() => {}} openAssistants={() => {}}/></div>;
}
createRoot(document.getElementById("root")!).render(<React.StrictMode><DialogProvider><CloseGuardProvider><MusicTest/></CloseGuardProvider></DialogProvider></React.StrictMode>);

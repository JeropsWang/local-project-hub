import { useState } from "react";
import { createRoot } from "react-dom/client";
import { Workbench } from "./components/Workbench";
import { TaskProvider } from "./business/TaskProvider";
import { WorkspacePage, type Workspace } from "./pages/WorkspacePage";
import "./css/workbench.css";

function App() {
  const [workspace, setWorkspace] = useState<Workspace>();
  if (!workspace) return <WorkspacePage onOpen={(view) => {
    window.location.hash = "#/projects";
    setWorkspace(view);
  }} />;
  return <TaskProvider key={workspace.workspaceId}>
    <Workbench workspace={workspace} onWorkspaceChanged={setWorkspace} />
  </TaskProvider>;
}

document.documentElement.dataset.theme = localStorage.getItem("theme") ?? "system";
document.documentElement.dataset.motion = localStorage.getItem("motion") ?? "on";
createRoot(document.getElementById("root")!).render(<App />);

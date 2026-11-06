import { useState } from "react";
import { createRoot } from "react-dom/client";
import { LibraryPage } from "./pages/LibraryPage";
import { WorkspacePage, type Workspace } from "./pages/WorkspacePage";
import "./css/workbench.css";

function App() {
  const [workspace, setWorkspace] = useState<Workspace>();
  return (
    <>
      <header className="appbar"><strong>Local Project Hub</strong><span>Windows 工作台</span></header>
      <main>{workspace ? <LibraryPage workspace={workspace} onWorkspaceChanged={setWorkspace} /> : <WorkspacePage onOpen={setWorkspace} />}</main>
    </>
  );
}

createRoot(document.getElementById("root")!).render(<App />);

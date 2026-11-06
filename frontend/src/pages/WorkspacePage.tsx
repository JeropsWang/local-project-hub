import { useState } from "react";
import { call, chooseWorkspaceDirectory } from "../services/client";

export interface Workspace {
  name: string;
  root: string;
  state: string;
}

export function WorkspacePage({ onOpen }: { onOpen: (view: Workspace) => void }) {
  const [root, setRoot] = useState("");
  const [name, setName] = useState("本地工作空间");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function open(create: boolean) {
    setBusy(true);
    setError("");
    try {
      const directory = root.trim() || await chooseWorkspaceDirectory();
      if (!directory) return;
      setRoot(directory);
      onOpen(await call<Workspace>("SelectWorkspace", { root: directory, name: name.trim() }, create));
    } catch (cause) {
      setError(String(cause));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="setup">
      <h1>选择工作空间</h1>
      <p>先选一个文件夹保存项目配置和运行记录。进入工作台后，可以查看本机 Docker 容器、登记项目和迁移配置。</p>
      <label>工作空间路径（可选）
        <input value={root} onChange={(event) => setRoot(event.target.value)} placeholder={"D:\\Workspaces\\ProjectHub"} />
      </label>
      <p>留空会打开 Windows 文件夹选择器。新建时请选择一个空目录，或在对话框里创建目录。</p>
      <label>工作空间名称<input value={name} onChange={(event) => setName(event.target.value)} /></label>
      <div className="actions">
        <button disabled={busy || !name.trim()} onClick={() => void open(true)}>选择目录并新建</button>
        <button className="secondary" disabled={busy} onClick={() => void open(false)}>打开已有工作空间</button>
      </div>
      {error && <p role="alert">{error}</p>}
    </section>
  );
}

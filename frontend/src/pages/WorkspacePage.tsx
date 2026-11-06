import { useState } from "react";
import { call } from "../services/client";

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
      onOpen(await call<Workspace>("SelectWorkspace", { root: root.trim(), name: name.trim() }, create));
    } catch (cause) {
      setError(String(cause));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="setup">
      <h1>选择工作空间</h1>
      <p>项目登记、独立环境和运行记录将保存在这里。新建时请选择空目录。</p>
      <label>工作空间路径<input value={root} onChange={(event) => setRoot(event.target.value)} placeholder="D:\\Workspaces\\ProjectHub" /></label>
      <label>工作空间名称<input value={name} onChange={(event) => setName(event.target.value)} /></label>
      <div className="actions">
        <button disabled={busy || !root.trim() || !name.trim()} onClick={() => void open(true)}>新建工作空间</button>
        <button className="secondary" disabled={busy || !root.trim()} onClick={() => void open(false)}>打开已有目录</button>
      </div>
      {error && <p role="alert">{error}</p>}
    </section>
  );
}

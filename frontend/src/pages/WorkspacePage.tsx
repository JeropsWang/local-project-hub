import { useState } from "react";
import { call, chooseWorkspaceDirectory } from "../services/client";

const workspaceErrors: Record<string, string> = {
  WORKSPACE_IDENTITY_INVALID: "此目录缺少有效的工作空间标识。首次使用时，请选择一个单独的空目录来新建工作空间。项目源码仍留在原目录。",
  WORKSPACE_NOT_EMPTY: "这个目录已有文件。请新建一个单独的空目录，或者打开之前创建的工作空间。",
  WORKSPACE_NOT_FOUND: "找不到这个目录。请重新浏览并选择之前创建的工作空间。",
  WORKSPACE_BUSY: "该工作空间已被使用。请先安全退出正在使用它的管理器。",
  WORKSPACE_DATABASE_MISSING: "工作空间缺少数据库文件。请检查是否复制了完整的工作空间目录。",
};

export interface Workspace {
  workspaceId: string;
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
      const detail = String(cause);
      const code = Object.keys(workspaceErrors).find((key) => detail.includes(key));
      setError(code ? workspaceErrors[code] : detail);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="setup">
      <h1>设置管理器工作空间</h1>
      <p>工作空间保存管理器自己的配置和运行记录。首次使用请选择单独的空目录，项目源码继续保存在原目录。</p>
      <label>工作空间路径（可选）
        <input value={root} onChange={(event) => setRoot(event.target.value)} placeholder={"D:\\Workspaces\\ProjectHub"} />
      </label>
      {root && <button type="button" className="secondary" disabled={busy} onClick={() => { setRoot(""); setError(""); }}>清空路径，重新选择</button>}
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

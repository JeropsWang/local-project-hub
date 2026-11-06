import { useState } from "react";
import { call, chooseWorkspaceDirectory } from "../services/client";
import styles from "../css/Workspace.module.css";

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

  async function browse() {
    setBusy(true);
    setError("");
    try {
      const directory = await chooseWorkspaceDirectory();
      if (directory) setRoot(directory);
    } catch (cause) {
      setError(String(cause));
    } finally {
      setBusy(false);
    }
  }

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
    <main className={styles.page}>
    <section className={styles.card} aria-labelledby="workspace-heading">
      <h1 id="workspace-heading">设置工作空间</h1>
      <p>工作空间保存管理器自己的配置和运行记录。首次使用请选择单独的空目录，项目源码继续保存在原目录。</p>
      <div className={styles.path}>
        <label>工作空间路径
          <input value={root} onChange={(event) => { setRoot(event.target.value); setError(""); }}
            placeholder={"D:\\Workspaces\\ProjectHub"} disabled={busy} />
        </label>
        <button autoFocus className="secondary" disabled={busy} onClick={() => void browse()}>选择文件夹</button>
      </div>
      <p>可以在选择器中创建新目录，也可以手动输入路径。</p>
      <label>工作空间名称<input value={name} onChange={(event) => setName(event.target.value)} /></label>
      <div className="actions">
        <button disabled={busy || !name.trim()} onClick={() => void open(true)}>创建工作空间</button>
        <button className="secondary" disabled={busy} onClick={() => void open(false)}>打开已有工作空间</button>
      </div>
      {error && <p role="alert">{error}</p>}
    </section>
    </main>
  );
}

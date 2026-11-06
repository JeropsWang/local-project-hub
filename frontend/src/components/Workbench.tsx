import { useEffect, useState } from "react";
import { useTasks } from "../business/TaskProvider";
import { call } from "../services/client";
import type { ProjectView } from "../services/models";
import { useRoute } from "../router/routes";
import { DockerPage } from "../pages/DockerPage";
import { TransferPage } from "../pages/TransferPage";
import { LibraryPage } from "../pages/LibraryPage";
import { ProjectPage } from "../pages/ProjectPage";
import { AddProjectPage } from "../pages/AddProjectPage";
import type { Workspace } from "../pages/WorkspacePage";
import { TaskPanel } from "./TaskPanel";
import styles from "../css/Shell.module.css";

const sections = [
  ["projects", "项目库"], ["docker", "Docker"], ["transfer", "配置迁移"], ["settings", "设置"],
] as const;

async function listProjects() {
  const all: ProjectView[] = [];
  for (let offset = 0; ; offset += 100) {
    const page = await call<ProjectView[] | null>("ListProjects", { offset, limit: 100 }) ?? [];
    all.push(...page);
    if (page.length < 100) return all;
  }
}

export function Workbench({ workspace, onWorkspaceChanged }: {
  workspace: Workspace;
  onWorkspaceChanged: (view: Workspace) => void;
}) {
  const { section, id, tab } = useRoute();
  const [projects, setProjects] = useState<ProjectView[]>([]);
  const [loading, setLoading] = useState(true);
  const [checks, setChecks] = useState<Record<string, string>>({});
  const [problem, setProblem] = useState("");
  const [retry, setRetry] = useState(0);
  const [moving, setMoving] = useState(false);
  const tasks = useTasks();
  const ready = workspace.state === "ready";
  const active = sections.find(([key]) => key === section) ?? sections[0];

  useEffect(() => {
    let disposed = false;
    listProjects().then((items) => {
      if (!disposed) { setProjects(items); setProblem(""); }
    }, (cause) => {
      if (!disposed) setProblem(String(cause));
    }).finally(() => {
      if (!disposed) setLoading(false);
    });
    return () => { disposed = true; };
  }, [workspace.workspaceId, retry, tasks.completed]);

  function refresh() {
    setRetry((count) => count + 1);
  }

  async function relocate() {
    setMoving(true);
    setProblem("");
    try {
      onWorkspaceChanged(await call<Workspace>("ConfirmRelocation", workspace.workspaceId));
      refresh();
    } catch (cause) {
      setProblem(String(cause));
    } finally {
      setMoving(false);
    }
  }

  return <div className={styles.shell}>
    <aside className={styles.sidebar}>
      <div className={styles.brand}>
        <span className={styles.brandIcon} aria-hidden="true">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7">
            <path d="M4 7h6l2 3h8v9H4z" /><path d="M4 7V5h10l2 3h4v2" />
          </svg>
        </span>
        <strong>Local Project Hub</strong>
      </div>
      <nav aria-label="工作台导航">
        {sections.map(([key, label]) => <a key={key} href={`#/${key}`}
          aria-current={active[0] === key ? "page" : undefined}>{label}</a>)}
      </nav>
      <div className={styles.workspace}><strong>{workspace.name}</strong><small title={workspace.root}>{workspace.root}</small></div>
    </aside>
    <div className={styles.body}>
      <header className={styles.topbar}>
        <span>{active[1]}</span>
        <button className="secondary" aria-expanded={tasks.open} onClick={() => tasks.setOpen(!tasks.open)}>
          任务{tasks.tasks.length > 0 ? ` · ${tasks.tasks.length}` : ""}
        </button>
      </header>
      <div className={styles.stage}>
        <main id="content" className={styles.content}>
          {!ready && <section className="notice" aria-label="工作空间迁移">
            <strong>工作空间需要重新绑定</strong>
            <p>确认迁移会清除旧机器的路径、凭据引用和容器授权，保留项目配置。</p>
            <button disabled={moving} onClick={() => void relocate()}>确认迁移到此目录</button>
          </section>}
          {problem && <div className="notice" role="alert"><p>{problem}</p><button className="secondary" onClick={refresh}>重试</button></div>}
          {active[0] === "docker" ? <DockerPage projects={projects} selectedID={id} /> :
            active[0] === "transfer" ? <TransferPage projects={projects} ready={ready} /> :
            active[0] === "settings" ? <Settings workspace={workspace} /> :
            id === "new" ? <AddProjectPage ready={ready} onChanged={refresh} /> :
            id ? <ProjectPage key={id} id={id} tab={tab} ready={ready} onChanged={refresh}
              onCheck={(projectId, revision, profileId, summary) => setChecks((previous) => ({
                ...previous, [`${projectId}:${revision}:${profileId}`]: summary,
              }))} /> :
            <LibraryPage projects={projects} loading={loading} ready={ready} checks={checks} />}
        </main>
        {tasks.open && <TaskPanel />}
      </div>
    </div>
  </div>;
}

function Settings({ workspace }: { workspace: Workspace }) {
  const tasks = useTasks();
  const [theme, setTheme] = useState(() => localStorage.getItem("theme") ?? "system");
  const [motion, setMotion] = useState(() => localStorage.getItem("motion") !== "off");

  function changeTheme(next: string) {
    setTheme(next);
    localStorage.setItem("theme", next);
    document.documentElement.dataset.theme = next;
  }

  function changeMotion(next: boolean) {
    setMotion(next);
    localStorage.setItem("motion", next ? "on" : "off");
    document.documentElement.dataset.motion = next ? "on" : "off";
  }

  return <>
    <h1>设置</h1>
    <section className="surface">
      <h2>工作空间</h2>
      <p>{workspace.name}</p>
      <p className="mono">{workspace.root}</p>
    </section>
    <section className="surface">
      <h2>外观</h2>
      <label>主题<select value={theme} onChange={(event) => changeTheme(event.target.value)}>
        <option value="system">跟随系统</option><option value="light">浅色</option><option value="dark">深色</option>
      </select></label>
      <label className="inline"><input type="checkbox" checked={motion} onChange={(event) => changeMotion(event.target.checked)} />启用封面动效</label>
    </section>
    <section className="surface">
      <h2>指定项目的接入进度</h2>
      <ul>
        <li>PersonWeb · 待验证隔离环境中的完整启停</li>
        <li>WeChatBot · 待接入管理协议并验证安全停止</li>
        <li>typing-tracker · 待接入管理协议并验证退出前保存</li>
      </ul>
    </section>
    <section className="surface">
      <h2>退出管理器</h2>
      <p>安全退出会先停止受管理的项目。关闭窗口会将工作台留在系统托盘。</p>
      <button className="secondary" onClick={() => void tasks.exit()}>安全退出</button>
    </section>
  </>;
}

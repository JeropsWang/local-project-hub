import { useEffect, useState } from "react";
import { resolveTask, submitPlan, type Plan, type TaskView } from "../business/tasks";
import { call, onExitRequested } from "../services/client";
import type { Workspace } from "./WorkspacePage";
import { ConfigurationEditor } from "./ConfigurationEditor";

interface Project {
  document: { projectId: string; name: string; group?: string; profiles: Record<string, { label: string }> };
  revision: number;
  runSummary: string;
}

interface ImportPreview {
  id: string;
  document: { name: string };
  requiredBindings?: string[];
}

interface Container {
  name: string;
  image: string;
  status: string;
  composeProject: string;
  relationship: string;
  projectIds: string[] | null;
}

export function LibraryPage({ workspace }: { workspace: Workspace }) {
  const [projects, setProjects] = useState<Project[]>([]);
  const [selected, setSelected] = useState("");
  const [profile, setProfile] = useState("");
  const [task, setTask] = useState<TaskView>();
  const [pending, setPending] = useState<{ plan: Plan; request: string }>();
  const [report, setReport] = useState<unknown>();
  const [archive, setArchive] = useState("");
  const [imported, setImported] = useState<ImportPreview>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [containers, setContainers] = useState<Container[]>();
  const project = projects.find((item) => item.document.projectId === selected);
  const profiles = Object.entries(project?.document.profiles ?? {});
  const ref = project && {
    projectId: project.document.projectId,
    profileId: profile || profiles[0]?.[0],
    expectedRevision: project.revision,
  };

  async function refresh() {
    setProjects((await call<Project[]>("Projects")) ?? []);
  }

  async function perform(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await action();
    } catch (cause) {
      setError(String(cause));
    } finally {
      setBusy(false);
    }
  }

  const exit = () => perform(async () => setTask(await call<TaskView>("RequestExit")));
  useEffect(() => {
    void refresh().catch((cause) => setError(String(cause)));
    return onExitRequested(() => void exit());
  }, []);

  useEffect(() => {
    if (!task || ["failed", "cancelled", "interrupted", "succeeded", "waiting-user"].includes(task.state)) return;
    const timer = window.setTimeout(() => {
      void call<TaskView>("Task", task.id).then((next) => {
        setTask(next);
        if (next.state === "succeeded") void refresh().catch((cause) => setError(String(cause)));
      }).catch((cause) => setError(String(cause)));
    }, 400);
    return () => window.clearTimeout(timer);
  }, [task]);

  function review(plan: Plan) {
    setPending({ plan, request: crypto.randomUUID() });
  }

  const taskActive = task && ["queued", "running", "cancel-requested"].includes(task.state);
  const disabled = busy || Boolean(taskActive) || workspace.state !== "ready";

  return (
    <div className="library">
      <aside>
        <h2>项目库</h2>
        {projects.length === 0 && <p>从配置包导入第一个项目。</p>}
        {projects.map((item) => (
          <button key={item.document.projectId} className="project" aria-pressed={selected === item.document.projectId}
            onClick={() => { setSelected(item.document.projectId); setProfile(""); setReport(undefined); }}>
            <strong>{item.document.name}</strong>
            <small>{item.document.group || "未分组"} · {item.runSummary}</small>
          </button>
        ))}
      </aside>
      <section className="details">
        <div className="heading">
          <div><h1>{project?.document.name || workspace.name}</h1><p>{workspace.root}</p></div>
          <button className="secondary" disabled={busy} onClick={() => void exit()}>安全退出</button>
        </div>
        {workspace.state !== "ready" && <p role="alert">这个工作空间需要重新绑定。</p>}
        <ConfigurationEditor key={project ? `${selected}:${project.revision}` : "new"}
          document={project?.document} revision={project?.revision} disabled={disabled}
          perform={perform} refresh={refresh} />
        {ref && (
          <section>
            <label>运行配置<select value={ref.profileId} onChange={(event) => setProfile(event.target.value)}>
              {profiles.map(([id, value]) => <option key={id} value={id}>{value.label}</option>)}
            </select></label>
            <div className="actions">
              <button className="secondary" disabled={disabled} onClick={() => void perform(async () => setReport(await call("Check", ref)))}>检测环境</button>
              {Object.entries({ prepare: "准备独立环境", start: "启动整个项目", stop: "停止项目" }).map(([kind, title]) =>
                <button key={kind} className={kind === "start" ? "" : "secondary"} disabled={disabled}
                  onClick={() => void perform(async () => review(await call<Plan>("Plan", kind, ref)))}>{title}</button>)}
            </div>
            {report !== undefined && <pre aria-label="环境检测结果">{JSON.stringify(report, null, 2)}</pre>}
          </section>
        )}
        <section>
          <h2>Docker 容器</h2>
          <button className="secondary" disabled={busy} onClick={() => void perform(async () =>
            setContainers(await call<Container[]>("Containers")))}>刷新 Docker 清单</button>
          {containers?.length === 0 && <p>当前 Docker 引擎没有容器。</p>}
          {containers?.map((container) => <div key={container.name} className="notice">
            <strong>{container.name}</strong>
            <p>{container.image} · {container.status} · {container.composeProject}</p>
            <p>{({ shared: "共享", managed: "受管", reference: "仅引用", untracked: "未关联" } as Record<string, string>)[container.relationship]}
              {container.projectIds?.map((id) => projects.find((item) => item.document.projectId === id)?.document.name || id).join("、") && " · "}
              {container.projectIds?.map((id) => projects.find((item) => item.document.projectId === id)?.document.name || id).join("、")}</p>
          </div>)}
        </section>
        <section>
          <h2>配置迁移</h2>
          <label>ZIP 文件路径<input value={archive} onChange={(event) => { setArchive(event.target.value); setImported(undefined); }} placeholder="D:\\Exports\\project.zip" /></label>
          <div className="actions">
            <button className="secondary" disabled={disabled || !archive.trim()} onClick={() => void perform(async () =>
              setImported(await call<ImportPreview>("PreviewImport", archive.trim())))}>预览导入</button>
            <button className="secondary" disabled={disabled || !ref || !archive.trim()} onClick={() => void perform(async () =>
              review(await call<Plan>("PlanExport", ref, archive.trim())))}>导出当前版本</button>
          </div>
          {imported && <div className="notice">
            <strong>{imported.document.name}</strong>
            <p>待绑定：{imported.requiredBindings?.join("、") || "无"}</p>
            <button disabled={disabled} onClick={() => void perform(async () =>
              review(await call<Plan>("PlanImport", { previewId: imported.id, bindings: [] })))}>登记项目</button>
          </div>}
        </section>
        {pending && <section className="notice">
          <h2>确认操作</h2>
          <ul>{pending.plan.steps?.map((step) => <li key={step.id}>{step.action} · {step.effect}</li>)}</ul>
          {pending.plan.warnings?.length ? <p>{pending.plan.warnings.join("、")}</p> : null}
          <div className="actions">
            <button disabled={disabled} onClick={() => void perform(async () => {
              setTask(await submitPlan(pending.plan, pending.request));
              setPending(undefined);
            })}>执行计划</button>
            <button className="secondary" onClick={() => setPending(undefined)}>返回</button>
          </div>
        </section>}
        {task && <section className="notice" aria-live="polite">
          <strong>{task.kind} · {task.state}</strong>
          {task.error && <p>{task.error.code} · {task.error.message}</p>}
          {task.state === "waiting-user" && <div className="actions">
            {Object.entries({ retry: "重试", "continue-wait": "继续等待", ...(task.kind === "exit" ? { "abort-exit": "取消退出" } : {}) })
              .map(([action, title]) => <button key={action} className="secondary" disabled={busy}
                onClick={() => void perform(async () => setTask(await resolveTask(task, action)))}>{title}</button>)}
          </div>}
        </section>}
        {error && <p role="alert">{error}</p>}
      </section>
    </div>
  );
}

import { useEffect, useRef, useState } from "react";
import { call } from "../services/client";
import type { CheckReport, ContainerView, ProfileRef, ProjectView } from "../services/models";
import type { Plan } from "../business/tasks";
import { useTasks } from "../business/TaskProvider";
import { projectError, runLabels } from "../business/projects";
import { containerHref, projectHref } from "../router/routes";
import { ProjectSettings } from "../components/ProjectSettings";
import { RunRecords } from "../components/RunRecords";
import styles from "../css/Projects.module.css";

const tabs = [["overview", "概览"], ["environment", "环境"], ["scripts", "启停脚本"], ["runs", "运行记录"]];
const activeStates = ["starting", "running", "stopping", "stop-failed", "interrupted"];

export function ProjectPage({ id, tab, ready, onChanged, onCheck }: {
  id: string;
  tab?: string;
  ready: boolean;
  onChanged: () => void;
  onCheck: (projectId: string, revision: number, profileId: string, summary: string) => void;
}) {
  const tasks = useTasks();
  const [project, setProject] = useState<ProjectView>();
  const [profile, setProfile] = useState("");
  const [report, setReport] = useState<CheckReport>();
  const [pending, setPending] = useState<Plan>();
  const [pendingTarget, setPendingTarget] = useState("");
  const [busy, setBusy] = useState(false);
  const operation = useRef(false);
  const [error, setError] = useState("");
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let disposed = false;
    call<ProjectView>("Project", id).then((value) => {
      if (!disposed) setProject(value);
    }, (cause) => {
      if (!disposed) setError(projectError(cause));
    });
    return () => { disposed = true; };
  }, [id, reload, tasks.completed]);

  const currentTab = tabs.some(([key]) => key === tab) ? tab : "overview";
  const profiles = project?.document.profiles ?? {};
  const profileID = profiles[profile] ? profile : Object.keys(profiles)[0];
  const ref: ProfileRef | undefined = project && profileID
    ? { projectId: id, profileId: profileID, expectedRevision: project.revision } : undefined;
  const runState = project?.runSummary ?? "unverified";
  const activeRun = activeStates.includes(runState);
  const inFlight = tasks.tasks.some((task) => task.projectId === id &&
    ["queued", "running", "waiting-user", "cancel-requested"].includes(task.state));
  const disabled = !ready || !ref || busy || inFlight;
  const selectedUnits = new Set<string>();
  function includeUnit(unitId: string) {
    if (!project || selectedUnits.has(unitId)) return;
    selectedUnits.add(unitId);
    project.document.units[unitId]?.dependsOn.forEach(includeUnit);
  }
  profiles[profileID]?.unitIds.forEach(includeUnit);
  const needsStop = [...selectedUnits].some((unitId) => {
    const unit = project?.document.units[unitId];
    return unit?.control === "managed" && (unit.kind === "process" || unit.kind === "compose") &&
      (!unit.stop || ["manual", "none"].includes(unit.stop.kind));
  });
  const blocked = !activeRun && (needsStop || (report && report.summary !== "ready"));

  async function perform(action: () => Promise<void>) {
    if (operation.current) return;
    operation.current = true;
    setBusy(true);
    setError("");
    try {
      await action();
    } catch (cause) {
      setError(projectError(cause));
    } finally {
      operation.current = false;
      setBusy(false);
    }
  }

  function check() {
    if (!ref) return;
    void perform(async () => {
      const next = await call<CheckReport>("Check", ref);
      setReport(next);
      onCheck(id, ref.expectedRevision, ref.profileId, next.summary);
    });
  }

  function plan(kind: "start" | "stop" | "prepare") {
    if (!ref) return;
    void perform(async () => {
      setPending(undefined);
      setPendingTarget("");
      let target = ref;
      if (kind === "stop") {
        const runs = await call<{ profileId: string; revision: number; state: string }[]>("Runs", id) ?? [];
        const active = runs.filter((run) => activeStates.includes(run.state));
        const run = active.find((item) => item.profileId === ref.profileId) ?? active[0];
        if (!run) throw new Error("找不到仍由管理器记录的运行实例。");
        target = { projectId: id, profileId: run.profileId, expectedRevision: run.revision };
        setPendingTarget(`${profiles[run.profileId]?.label ?? run.profileId} · 启动时配置版本 ${run.revision}`);
      }
      let next: Plan;
      try {
        next = await call<Plan>("Plan", kind, target);
      } catch (cause) {
        if (kind === "start" && String(cause).includes("CHECKS_NOT_READY")) {
          const latest = await call<CheckReport>("Check", ref);
          setReport(latest);
          onCheck(id, ref.expectedRevision, ref.profileId, latest.summary);
          window.location.hash = projectHref(id, "environment");
        }
        throw cause;
      }
      if (next.requiredBindings?.length && kind === "start") {
        throw new Error(`请先补齐本机绑定：${next.requiredBindings.join("、")}`);
      }
      if (kind === "start" && !next.warnings?.length) {
        await tasks.submit({ ...next, projectId: id });
      } else {
        setPending({ ...next, projectId: id });
      }
    });
  }

  function saved(value: ProjectView) {
    setProject(value);
    setReport(undefined);
    setPending(undefined);
    onChanged();
  }

  if (!project) return <>
    <a href="#/projects">返回项目库</a>
    {error ? <p role="alert">{error}</p> : <p role="status">正在读取项目…</p>}
  </>;

  return <>
    <a href="#/projects">返回项目库</a>
    <div className="toolbar">
      <div>
        <h1>{project.document.name}</h1>
        <p>{project.document.group || "未分组"} · 配置版本 {project.revision}</p>
        <span className="badge" data-state={runState}>{runLabels[runState] ?? runState}</span>
      </div>
      <button disabled={disabled || blocked || runState === "stopping" || runState === "starting"}
        onClick={() => plan(activeRun ? "stop" : "start")}>{activeRun ? "停止项目" : "启动项目"}</button>
    </div>
    {inFlight && <p role="status">此项目有待处理任务。请查看顶部任务面板。</p>}
    {!activeRun && needsStop && <p>启动前请先<a href={projectHref(id, "scripts")}>配置安全停止方式</a>。</p>}
    {!activeRun && report && report.summary !== "ready" && <p>环境仍有待处理项。<a href={projectHref(id, "environment")}>查看并重新检测</a></p>}
    {!ready && <p>请先确认工作空间迁移，再执行项目操作。</p>}
    <label>运行配置<select value={profileID ?? ""} disabled={busy || activeRun} onChange={(event) => {
      setProfile(event.target.value);
      setReport(undefined);
      setPending(undefined);
      setError("");
    }}>
      {Object.entries(profiles).map(([key, value]) => <option key={key} value={key}>{value.label}</option>)}
    </select></label>
    {error && <p role="alert">{error}</p>}
    {pending && <section className="notice" aria-label="确认操作">
      <h2>确认操作</h2>
      {pendingTarget && <p>停止目标：{pendingTarget}</p>}
      {pending.steps?.length ? <ul>{pending.steps.map((step) => <li key={step.id}>{step.action} · {step.effect}</li>)}</ul> : null}
      {pending.warnings?.map((warning) => <p key={warning}>{warning}</p>)}
      <div className="actions">
        <button disabled={disabled} onClick={() => void perform(async () => {
          await tasks.submit(pending);
          setPending(undefined);
        })}>确认并执行</button>
        <button className="secondary" onClick={() => setPending(undefined)}>取消</button>
      </div>
    </section>}
    <nav className={styles.tabs} aria-label="项目详情">
      {tabs.map(([key, label]) => <a key={key} href={projectHref(id, key)}
        aria-current={currentTab === key ? "page" : undefined}>{label}</a>)}
    </nav>
    {currentTab === "overview" && <Overview project={project} report={report} />}
    {currentTab === "environment" && <>
      <div className="actions">
        <button className="secondary" disabled={disabled} onClick={check}>仅检测</button>
        <button className="secondary" disabled={disabled} onClick={() => plan("prepare")}>准备独立环境</button>
      </div>
      {report ? <>
        <p role="status">{report.summary === "ready" ? "环境检查通过" : "环境仍有待处理项"}</p>
        {report.items?.map((item) => {
          const requirement = project.document.requirements[item.requirementId];
          return <section className="surface" key={item.requirementId}>
            <h3>{requirement?.component || item.requirementId}</h3>
            <p>{({ satisfied: "满足", missing: "缺失", incompatible: "不兼容", "binding-required": "待绑定", unknown: "待核实" } as Record<string, string>)[item.status] ?? item.status}
              {requirement?.range && ` · 所需版本 ${requirement.range}`}</p>
            {item.evidence?.map((entry, index) => <p key={index}>{entry.version ? `检测值 ${entry.version} · ` : ""}{entry.source}</p>)}
            {item.reasons?.map((reason, index) => <p key={index}>{reason.message || reason.code}</p>)}
            {item.repairs?.map((repair) => <p key={repair.id}>{repair.label} · {repair.effect}</p>)}
          </section>;
        })}
      </> : <p>尚未检测当前配置。点击「仅检测」查看版本、缺项和修复步骤。</p>}
    </>}
    {currentTab === "scripts" && <ProjectSettings key={project.revision} project={project} ready={ready}
      onSaved={saved} onReload={() => { setReport(undefined); setPending(undefined); setReload((value) => value + 1); }} />}
    {currentTab === "runs" && <RunRecords projectId={id} refresh={tasks.completed} />}
  </>;
}

function Overview({ project, report }: { project: ProjectView; report?: CheckReport }) {
  const id = project.document.projectId;
  const [containers, setContainers] = useState<ContainerView[]>();
  const [message, setMessage] = useState("");
  const [loading, setLoading] = useState(false);

  async function load() {
    setLoading(true);
    setMessage("");
    try {
      const items = await call<ContainerView[]>("Containers") ?? [];
      setContainers(items.filter((container) => container.projectIds?.includes(id)));
    } catch (cause) {
      setMessage(`无法读取 Docker 清单：${String(cause)}`);
    } finally {
      setLoading(false);
    }
  }

  const units = Object.entries(project.document.units);
  return <>
    <h2>依赖与待处理项</h2>
    {report?.summary && report.summary !== "ready" && <p><a href={projectHref(id, "environment")}>查看环境缺项和修复步骤</a></p>}
    <ul>{units.map(([key, unit]) => <li key={key}>{unit.label} · {unit.kind}
      {unit.dependsOn.length > 0 && ` · 依赖 ${unit.dependsOn.map((dep) => project.document.units[dep]?.label ?? dep).join("、")}`}
      {unit.kind === "process" && unit.control === "managed" && unit.stop?.kind === "manual" &&
        <> · <a href={projectHref(id, "scripts")}>需要配置安全停止方式</a></>}</li>)}</ul>
    <h2>关联容器</h2>
    <button className="secondary" disabled={loading} onClick={() => void load()}>{loading ? "读取中…" : "查看关联容器"}</button>
    {message && <p role="alert">{message}</p>}
    {containers?.length === 0 && <p>目前没有已核实关联的容器。</p>}
    {containers?.length ? <ul>{containers.map((container) => <li key={container.id}>
      <a href={containerHref(container.id)}>{container.name}</a>
      {container.shared && " · 共享"}
    </li>)}</ul> : null}
  </>;
}

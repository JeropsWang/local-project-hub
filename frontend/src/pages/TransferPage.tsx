import { useRef, useState } from "react";
import { useTasks } from "../business/TaskProvider";
import type { Plan } from "../business/tasks";
import {
  collectImportBindings,
  documentBindingRequirements,
  transferError,
  transferWarning,
  type BindingDraft,
} from "../business/transfer";
import { BindingFields } from "../components/BindingFields";
import { projectHref } from "../router/routes";
import { chooseImportArchive } from "../services/client";
import type { ProjectView } from "../services/models";
import { chooseExportArchive, planExport, planImport, previewImport, type ImportPreview } from "../services/transfer";
import styles from "../css/TransferPage.module.css";

type Operation = "import" | "export";

interface Pending {
  kind: Operation;
  plan: Plan;
  name: string;
  path: string;
}

interface TransferPageProps {
  projects: ProjectView[];
  ready: boolean;
}

export function TransferPage({ projects, ready }: TransferPageProps) {
  const { submit, tasks, setOpen } = useTasks();
  const [archive, setArchive] = useState("");
  const [preview, setPreview] = useState<ImportPreview>();
  const [values, setValues] = useState<Record<string, BindingDraft>>({});
  const [exportID, setExportID] = useState("");
  const [selectedProfile, setSelectedProfile] = useState("");
  const [pending, setPending] = useState<Pending>();
  const [busy, setBusy] = useState<Operation>();
  const [error, setError] = useState<{ kind: Operation; message: string }>();
  const [submitted, setSubmitted] = useState<Partial<Record<Operation, string>>>({});
  const inFlight = useRef(false);

  const requirements = preview ? documentBindingRequirements(preview.document, preview.requiredBindings ?? []) : [];
  const includedFiles = Object.values(preview?.document.files ?? {})
    .flatMap((file) => file.availability === "included" && file.bundlePath ? [file.bundlePath] : []);
  const duplicate = preview && projects.find(({ document }) => document.projectId === preview.document.projectId);
  const target = projects.find(({ document }) => document.projectId === exportID);
  const profiles = Object.entries(target?.document.profiles ?? {});
  const profileId = profiles.some(([id]) => id === selectedProfile) ? selectedProfile : profiles[0]?.[0];
  const exitPending = tasks.some((task) =>
    task.kind === "exit" && ["queued", "running", "waiting-user"].includes(task.state));
  const disabled = !ready || !!busy || exitPending;
  const importTask = tasks.find((task) => task.id === submitted.import);
  const exportTask = tasks.find((task) => task.id === submitted.export);
  const activeStates = ["queued", "running", "cancel-requested", "waiting-user"];
  const importing = !!importTask && activeStates.includes(importTask.state);
  const exporting = !!exportTask && activeStates.includes(exportTask.state);

  function discardPlan(kind: Operation) {
    setPending((plan) => plan?.kind === kind ? undefined : plan);
  }

  async function perform(kind: Operation, action: () => Promise<void>) {
    if (inFlight.current || !ready || exitPending) return;
    inFlight.current = true;
    setBusy(kind);
    setError(undefined);
    try {
      await action();
    } catch (cause) {
      setError({ kind, message: transferError(cause) });
    } finally {
      inFlight.current = false;
      setBusy(undefined);
    }
  }

  async function readPreview(path: string) {
    setArchive(path);
    setPreview(undefined);
    setValues({});
    discardPlan("import");
    setSubmitted((current) => ({ ...current, import: undefined }));
    setPreview(await previewImport(path));
  }

  function planImportReview() {
    return perform("import", async () => {
      if (!preview) return;
      if (Date.parse(preview.expiresAt) <= Date.now()) throw new Error("IMPORT_PREVIEW_EXPIRED");
      const plan = await planImport(preview.id, collectImportBindings(requirements, values));
      setSubmitted((current) => ({ ...current, import: undefined }));
      setPending({ kind: "import", plan, name: preview.document.name, path: archive });
    });
  }

  function planExportReview() {
    return perform("export", async () => {
      if (!target || !profileId) return;
      const path = await chooseExportArchive(target.document.name);
      if (!path) return;
      const plan = await planExport({
        projectId: target.document.projectId,
        profileId,
        expectedRevision: target.revision,
      }, path);
      setSubmitted((current) => ({ ...current, export: undefined }));
      setPending({ kind: "export", plan, name: target.document.name, path });
    });
  }

  function confirmation(kind: Operation) {
    if (pending?.kind !== kind) return null;
    const unresolved = pending.plan.requiredBindings ?? [];
    return (
      <div className={styles.confirmation}>
        <h3>{kind === "import" ? "确认登记" : "确认导出"}</h3>
        <p><strong>{pending.name}</strong><br /><code>{pending.path}</code></p>
        {unresolved.length > 0 && (
          <p>{kind === "import" ? "登记后仍待绑定" : "目标机器需要重新绑定"}：{unresolved.join("、")}</p>
        )}
        {pending.plan.warnings?.map((warning) => <p key={warning}>{transferWarning(warning)}</p>)}
        <div className="actions">
          <button type="button" disabled={disabled} onClick={() => void perform(kind, async () => {
            const task = await submit(pending.plan);
            setSubmitted((current) => ({ ...current, [kind]: task.id }));
            setPending(undefined);
          })}>{kind === "import" ? "确认登记" : "生成配置包"}</button>
          <button className="secondary" type="button" disabled={!!busy} onClick={() => discardPlan(kind)}>返回修改</button>
        </div>
      </div>
    );
  }

  function taskStatus(kind: Operation) {
    const task = tasks.find((item) => item.id === submitted[kind]);
    if (!task) return null;
    if (task.state === "succeeded") {
      return (
        <p role="status">
          {kind === "export" ? "配置包已导出。" : "项目已登记。"}
          {kind === "import" && preview && <a href={projectHref(preview.document.projectId)}>打开项目</a>}
        </p>
      );
    }
    if (task.state === "failed") {
      return <p role="alert">操作失败。{transferError(task.error?.code ?? "请在任务面板查看原因。")}</p>;
    }
    if (task.state === "cancelled" || task.state === "interrupted") {
      return <p role="status">{task.state === "cancelled" ? "任务已取消。" : "任务已中断。"}</p>;
    }
    return <p role="status">已提交任务。<button className="secondary" type="button" onClick={() => setOpen(true)}>查看进度</button></p>;
  }

  return (
    <section aria-labelledby="transfer-heading">
      <div className="toolbar">
        <div>
          <h1 id="transfer-heading">配置迁移</h1>
          <p>在不同 Windows 工作空间之间迁移项目配置。</p>
        </div>
      </div>
      {!ready && <p role="status">请先在设置中确认工作空间迁移。</p>}
      {exitPending && <p role="status">正在安全退出，暂时无法导入或导出。</p>}
      <div className={styles.layout}>
        <section className={styles.panel} aria-labelledby="import-heading">
          <h2 id="import-heading">导入配置包</h2>
          <p>选择由 Local Project Hub 导出的 ZIP。GitHub 源码 ZIP 解压后，请<a href="#/projects/new">从目录添加</a>。</p>
          <form onSubmit={(event) => {
            event.preventDefault();
            if (archive.trim() && !importing) void perform("import", () => readPreview(archive.trim()));
          }}>
            <label>
              配置包路径
              <input value={archive} disabled={disabled || importing} placeholder={"D:\\Exports\\project.zip"} onChange={(event) => {
                setArchive(event.target.value);
                setPreview(undefined);
                setValues({});
                setError(undefined);
                discardPlan("import");
                setSubmitted((current) => ({ ...current, import: undefined }));
              }} />
            </label>
            <div className="actions">
              <button type="button" disabled={disabled || importing} onClick={() => void perform("import", async () => {
                const path = await chooseImportArchive();
                if (path) await readPreview(path);
              })}>选择 ZIP 并预览</button>
              <button className="secondary" type="submit" disabled={disabled || importing || !archive.trim()}>预览填写的路径</button>
            </div>
          </form>
          {busy === "import" && <p role="status">正在处理导入…</p>}
          {error?.kind === "import" && <p role="alert">{error.message}</p>}
          {preview && (
            <section className={styles.preview} aria-label="配置包预览">
              <h3>{preview.document.name}</h3>
              <p>格式 {preview.document.schemaVersion} · {preview.document.group || "未分组"}</p>
              <p>运行配置：{Object.values(preview.document.profiles ?? {}).map((profile) => profile.label).join("、") || "无"}</p>
              {includedFiles.length > 0 && (
                <details>
                  <summary>查看包内文件（{includedFiles.length}）</summary>
                  <ul>{includedFiles.map((file) => <li key={file}><code>{file}</code></li>)}</ul>
                </details>
              )}
              {preview.warnings?.map((warning) => <p key={warning}>{transferWarning(warning)}</p>)}
              {duplicate && !submitted.import && (
                <p role="alert">此项目已登记。<a href={projectHref(duplicate.document.projectId)}>打开已有项目</a></p>
              )}
              {!duplicate && requirements.length > 0 && importTask?.state !== "succeeded" && (
                <BindingFields
                  requirements={requirements}
                  values={values}
                  disabled={disabled || importing}
                  onChange={(key, draft) => {
                    setValues((current) => ({ ...current, [key]: draft }));
                    discardPlan("import");
                  }}
                />
              )}
              {!duplicate && pending?.kind !== "import" && !importing && importTask?.state !== "succeeded" && (
                <button type="button" disabled={disabled} onClick={() => void planImportReview()}>核对登记计划</button>
              )}
              {confirmation("import")}
              {taskStatus("import")}
            </section>
          )}
        </section>
        <section className={styles.panel} aria-labelledby="export-heading">
          <h2 id="export-heading">导出当前配置</h2>
          <p>保存项目定义和声明包含的文件。目标工作空间需要重新绑定本机路径、凭据和容器资源。</p>
          {projects.length === 0 ? (
            <p>登记第一个项目后即可导出。<a href="#/projects">打开项目库</a></p>
          ) : (
            <>
              <label>
                项目
                <select disabled={disabled || exporting} value={target?.document.projectId ?? ""} onChange={(event) => {
                  setExportID(event.target.value);
                  setSelectedProfile("");
                  discardPlan("export");
                }}>
                  <option value="">选择项目</option>
                  {projects.map(({ document }) => <option key={document.projectId} value={document.projectId}>{document.name}</option>)}
                </select>
              </label>
              {target && (
                <label>
                  运行配置
                  <select disabled={disabled || exporting} value={profileId ?? ""} onChange={(event) => {
                    setSelectedProfile(event.target.value);
                    discardPlan("export");
                  }}>
                    {profiles.map(([id, profile]) => <option key={id} value={id}>{profile.label}</option>)}
                  </select>
                </label>
              )}
              <button type="button" disabled={disabled || exporting || !profileId} onClick={() => void planExportReview()}>选择保存位置</button>
              {busy === "export" && <p role="status">正在处理导出…</p>}
              {error?.kind === "export" && <p role="alert">{error.message}</p>}
              {confirmation("export")}
              {taskStatus("export")}
            </>
          )}
        </section>
      </div>
    </section>
  );
}

import { useEffect, useState, type FormEvent } from "react";
import { call } from "../services/client";
import type { Binding, ProjectView } from "../services/models";
import { buildCommand, editCommand, emptyCommand, projectError, type CommandDraft } from "../business/projects";
import { collectImportBindings, documentBindingRequirements, type BindingDraft } from "../business/transfer";
import { ConfigurationEditor } from "./ConfigurationEditor";
import { CommandFields } from "./CommandFields";
import { BindingFields } from "./BindingFields";

export function ProjectSettings({ project, ready, onSaved, onReload }: {
  project: ProjectView;
  ready: boolean;
  onSaved: (project: ProjectView) => void;
  onReload: () => void;
}) {
  const { document, revision } = project;
  const units = Object.entries(document.units).filter(([, unit]) => unit.kind === "process" && unit.start);
  const [unitId, setUnitId] = useState(units[0]?.[0] ?? "");
  const requirements = documentBindingRequirements(document);

  return <>
    <h2>启停脚本与本机配置</h2>
    <p>保存生成新版本。正在运行的项目继续使用启动时记录的配置。</p>
    <p>如需随启动固定脚本文件内容，请在高级配置中声明脚本文件并使用文件引用。直接传入脚本路径时，运行期间修改文件仍可能影响它。</p>
    {units.length > 0 ? <>
      <label>进程<select value={unitId} onChange={(event) => setUnitId(event.target.value)}>
        {units.map(([id, unit]) => <option key={id} value={id}>{unit.label}</option>)}
      </select></label>
      <CommandEditor key={unitId} project={project} unitId={unitId} ready={ready} onSaved={onSaved} onReload={onReload} />
    </> : <p>这个项目没有可通过表单编辑的进程。请通过高级 JSON 查看多服务配置。</p>}
    {requirements.length > 0 && <LocalBindings project={project} ready={ready} onSaved={onSaved} />}
    <Appearance project={project} ready={ready} onSaved={onSaved} />
    <Advanced document={document} revision={revision} ready={ready} onSaved={onReload} />
  </>;
}

function CommandEditor({ project, unitId, ready, onSaved, onReload }: {
  project: ProjectView;
  unitId: string;
  ready: boolean;
  onSaved: (project: ProjectView) => void;
  onReload: () => void;
}) {
  const unit = project.document.units[unitId];
  const [start, setStart] = useState(() => unit.start ? editCommand(unit.start) : emptyCommand);
  const [stop, setStop] = useState(() => unit.stop?.command ? editCommand(unit.stop.command) : emptyCommand);
  const [mode, setMode] = useState(unit.stop?.kind === "adapter" ? "adapter" : unit.stop?.command ? "command" : "manual");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function save(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const next = {
        ...unit,
        start: buildCommand(start, unit.start),
        stop: mode === "adapter" ? unit.stop : mode === "command"
          ? { kind: "command", command: buildCommand(stop, unit.stop?.command), timeoutSeconds: unit.stop?.timeoutSeconds ?? 30 }
          : { kind: "manual", reason: "需要配置安全停止命令" },
      };
      onSaved(await call<ProjectView>("Save", {
        document: { ...project.document, units: { ...project.document.units, [unitId]: next } },
        bindings: [],
        expectedRevision: project.revision,
      }));
    } catch (cause) {
      setError(projectError(cause));
    } finally {
      setBusy(false);
    }
  }

  return <form onSubmit={(event) => void save(event)}>
    <fieldset disabled={!ready || busy}>
      <legend>运行命令</legend>
      <CommandFields title="启动命令" value={start} onChange={setStart} />
      <label>停止方式<select value={mode} onChange={(event) => setMode(event.target.value)}>
        {unit.stop?.kind === "adapter" && <option value="adapter">保留已配置的管理协议</option>}
        <option value="command">执行停止命令</option>
        <option value="manual">待补安全停止方式</option>
      </select></label>
      {mode === "command" && <CommandFields title="停止命令" value={stop} onChange={setStop} />}
      <button type="submit">保存启停配置</button>
    </fieldset>
    {error && <p role="alert">{error}</p>}
    {error.startsWith("配置已更新") && <button className="secondary" type="button" onClick={onReload}>重新加载并放弃当前草稿</button>}
  </form>;
}

function LocalBindings({ project, ready, onSaved }: {
  project: ProjectView;
  ready: boolean;
  onSaved: (project: ProjectView) => void;
}) {
  const requirements = documentBindingRequirements(project.document);
  const [values, setValues] = useState<Record<string, BindingDraft>>({});
  const [loading, setLoading] = useState(ready);
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const { projectId } = project.document;
  useEffect(() => {
    if (!ready) return;
    let disposed = false;
    setLoading(true);
    Promise.all(requirements.map(({ key }) => call<Binding | null>("Binding", projectId, key)))
      .then((entries) => {
        if (disposed) return;
        const next: Record<string, BindingDraft> = {};
        for (const binding of entries) {
          if (binding) next[binding.key] = { kind: binding.kind, value: binding.localLocator ?? binding.secretRef ?? "" };
        }
        setValues(next);
      }, (cause) => {
        if (!disposed) setError(projectError(cause));
      }).finally(() => { if (!disposed) setLoading(false); });
    return () => { disposed = true; };
    // This component is remounted by the editor when the project revision changes.
  }, [projectId, ready]);

  async function save() {
    setBusy(true);
    setError("");
    try {
      const bindings = collectImportBindings(requirements, values);
      onSaved(await call<ProjectView>("Save", { document: project.document, bindings, expectedRevision: project.revision }));
    } catch (cause) {
      setError(projectError(cause));
    } finally {
      setBusy(false);
    }
  }

  return <section className="surface">
    {loading && <p role="status">正在读取本机绑定…</p>}
    <BindingFields requirements={requirements} values={values} disabled={!ready || loading || busy}
      onChange={(key, draft) => { setValues((previous) => ({ ...previous, [key]: draft })); setDirty(true); }} />
    <p>留空的字段不会覆盖已有绑定。</p>
    <button disabled={!ready || loading || busy || !dirty} onClick={() => void save()}>保存本机绑定</button>
    {error && <p role="alert">{error}</p>}
  </section>;
}

async function coverPayload(file: File) {
  const formats: Record<string, string> = { "image/png": "png", "image/jpeg": "jpg", "image/webp": "webp" };
  const extension = formats[file.type];
  if (!extension || file.size === 0 || file.size > 1024 * 1024) {
    throw new Error("请选择 1 MiB 以内的 PNG、JPEG 或 WebP 图片。");
  }
  const data = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).split(",", 2)[1]);
    reader.onerror = () => reject(new Error("无法读取图片。"));
    reader.readAsDataURL(file);
  });
  return { extension, data };
}

function Appearance({ project, ready, onSaved }: {
  project: ProjectView;
  ready: boolean;
  onSaved: (project: ProjectView) => void;
}) {
  const [file, setFile] = useState<File>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function save() {
    if (!file) return;
    setBusy(true);
    setError("");
    try {
      const { extension, data } = await coverPayload(file);
      const document = project.document;
      const id = document.appearance?.coverFileId ?? `cover-${crypto.randomUUID()}`;
      const path = `files/covers/${id}.${extension}`;
      onSaved(await call<ProjectView>("Save", {
        document: {
          ...document,
          appearance: { ...document.appearance, coverFileId: id },
          files: { ...document.files, [id]: { availability: "included", bundlePath: path, required: false, role: "cover" } },
        },
        bindings: [],
        includedFiles: { [path]: data },
        expectedRevision: project.revision,
      }));
    } catch (cause) {
      setError(projectError(cause));
    } finally {
      setBusy(false);
    }
  }

  return <section className="surface">
    <h2>项目封面</h2>
    <label>选择图片<input type="file" accept="image/png,image/jpeg,image/webp" disabled={!ready || busy}
      onChange={(event) => setFile(event.target.files?.[0])} /></label>
    <button disabled={!ready || busy || !file} onClick={() => void save()}>保存封面</button>
    {error && <p role="alert">{error}</p>}
  </section>;
}

function Advanced({ document, revision, ready, onSaved }: {
  document: ProjectView["document"];
  revision: number;
  ready: boolean;
  onSaved: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function perform(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await action();
    } catch (cause) {
      setError(projectError(cause));
    } finally {
      setBusy(false);
    }
  }
  return <>
    <ConfigurationEditor document={document} revision={revision} disabled={!ready || busy}
      perform={perform} refresh={async () => onSaved()} />
    {error && <p role="alert">{error}</p>}
  </>;
}

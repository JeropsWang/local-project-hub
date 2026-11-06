import { useState, type FormEvent } from "react";
import { call, chooseWorkspaceDirectory } from "../services/client";
import type { Binding, ProjectView } from "../services/models";
import { chooseBindingLocation } from "../services/transfer";
import { bindExecutable, createProject, emptyCommand, projectError } from "../business/projects";
import { projectHref } from "../router/routes";
import { CommandFields } from "../components/CommandFields";
import { ConfigurationEditor } from "../components/ConfigurationEditor";

interface Inspection { name: string; markers: string[] }

export function AddProjectPage({ ready, onChanged }: { ready: boolean; onChanged: () => void }) {
  const [mode, setMode] = useState("directory");
  const [folder, setFolder] = useState("");
  const [inspection, setInspection] = useState<Inspection>();
  const [marker, setMarker] = useState("");
  const [name, setName] = useState("");
  const [group, setGroup] = useState("");
  const [start, setStart] = useState(emptyCommand);
  const [stop, setStop] = useState(emptyCommand);
  const [hasStop, setHasStop] = useState(false);
  const [executables, setExecutables] = useState<Record<string, Binding>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function perform(action: () => Promise<void>) {
    if (busy || !ready) return;
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

  async function inspect(path: string) {
    const result = await call<Inspection>("InspectSource", path);
    setInspection(result);
    setMarker("");
    setName((current) => current || result.name);
  }

  function register(event: FormEvent) {
    event.preventDefault();
    void perform(async () => {
      if (!inspection || !name.trim()) throw new Error("请选择源码目录并填写项目名称。");
      const keys = [start, ...(hasStop ? [stop] : [])].filter((item) => item.mode === "bound").map((item) => item.locator);
      const bindings = Object.values(executables).filter((item) => keys.includes(item.key));
      const request = createProject(name, group, folder.trim(), start, hasStop ? stop : undefined, bindings);
      const project = await call<ProjectView>("Register", request);
      onChanged();
      window.location.hash = projectHref(project.document.projectId);
    });
  }

  async function browseExecutable(kind: "start" | "stop") {
    const path = await chooseBindingLocation("runtime");
    if (!path) return;
    const key = `${kind}-executable`;
    const { binding, command } = bindExecutable(path, key, kind === "start" ? start : stop);
    setExecutables((previous) => ({ ...previous, [key]: binding }));
    if (kind === "start") setStart(command);
    else setStop(command);
  }

  return <>
    <a href="#/projects">返回项目库</a>
    <h1>添加项目</h1>
    <p>先选择已解压的源码目录，再确认项目的启动与停止方式。</p>
    <div className="actions">
      <button className={mode === "directory" ? "" : "secondary"} aria-pressed={mode === "directory"}
        onClick={() => setMode("directory")}>从目录添加</button>
      <a className="button secondary" href="#/transfer">导入配置包</a>
      <button className={mode === "json" ? "" : "secondary"} aria-pressed={mode === "json"}
        onClick={() => setMode("json")}>高级 JSON</button>
    </div>
    {error && <p role="alert">{error}</p>}
    {mode === "json" ? <ConfigurationEditor disabled={busy || !ready} perform={perform}
      refresh={async () => { onChanged(); window.location.hash = "#/projects"; }} /> : <form onSubmit={register}>
      <fieldset className="surface" disabled={busy || !ready}>
        <legend>源码目录</legend>
        <label>本地路径<input value={folder} onChange={(event) => { setFolder(event.target.value); setInspection(undefined); }} /></label>
        <div className="actions">
          <button type="button" onClick={() => void perform(async () => {
            const path = await chooseWorkspaceDirectory();
            if (path) { setFolder(path); setInspection(undefined); await inspect(path); }
          })}>选择文件夹</button>
          <button type="button" className="secondary" disabled={!folder.trim()}
            onClick={() => void perform(() => inspect(folder.trim()))}>识别此目录</button>
        </div>
        {inspection && <>
          {inspection.markers.length ? <label>识别到的项目文件<select value={marker} onChange={(event) => setMarker(event.target.value)}>
            <option value="">选择参考入口</option>
            {inspection.markers.map((item) => <option key={item}>{item}</option>)}
          </select></label> : <p>没有识别到常见项目文件，仍可手动配置本地进程。</p>}
          {marker && <p>已选 <code>{marker}</code> 作为参考。请根据项目说明填写实际命令。</p>}
          {inspection.markers.some((item) => /compose/i.test(item)) &&
            <p>发现 Compose 配置。多服务项目请使用配置包或高级 JSON 登记。</p>}
        </>}
      </fieldset>
      {inspection && <fieldset disabled={busy || !ready}>
        <legend>运行配置</legend>
        <label>项目名称<input required maxLength={120} value={name} onChange={(event) => setName(event.target.value)} /></label>
        <label>分组<input maxLength={80} value={group} placeholder="未分组" onChange={(event) => setGroup(event.target.value)} /></label>
        <CommandFields title="启动命令" value={start} onChange={setStart} simple />
        <button type="button" className="secondary" onClick={() => void perform(() => browseExecutable("start"))}>选择本机启动程序</button>
        {start.mode === "bound" && executables[start.locator] && <p className="mono">{executables[start.locator].localLocator}</p>}
        <label className="inline"><input type="checkbox" checked={hasStop} onChange={(event) => setHasStop(event.target.checked)} />填写安全停止命令</label>
        {hasStop ? <>
          <CommandFields title="停止命令" value={stop} onChange={setStop} simple />
          <button type="button" className="secondary" onClick={() => void perform(() => browseExecutable("stop"))}>选择本机停止程序</button>
          {stop.mode === "bound" && executables[stop.locator] && <p className="mono">{executables[stop.locator].localLocator}</p>}
        </> :
          <p>可以先登记，补齐停止方式后再由管理器启动。</p>}
        <button type="submit">登记项目</button>
      </fieldset>}
    </form>}
  </>;
}

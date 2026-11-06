import type { CommandDraft } from "../business/projects";

export function CommandFields({ title, value, onChange, simple = false }: {
  title: string;
  value: CommandDraft;
  onChange: (value: CommandDraft) => void;
  simple?: boolean;
}) {
  return <fieldset className="surface">
    <legend>{title}</legend>
    <label>可执行项类型<select value={value.mode} onChange={(event) =>
      onChange({ ...value, mode: event.target.value as CommandDraft["mode"], target: "", locator: simple ? "source" : value.locator })}>
      <option value="bound">{simple && value.locator !== "source" ? "已选择的本机程序" : "目录中的可执行文件"}</option>
      {!simple && <option value="runtime">已配置的运行时</option>}
      <option value="system">Windows 系统工具</option>
    </select></label>
    {!simple && value.mode !== "system" && <label>绑定键或运行时 ID
      <input value={value.locator} onChange={(event) => onChange({ ...value, locator: event.target.value })} />
    </label>}
    {value.mode === "system" ? <label>系统工具<select required value={value.target}
      onChange={(event) => onChange({ ...value, target: event.target.value })}>
      <option value="">请选择</option>
      {["powershell", "pwsh", "cmd", "docker", "git"].map((tool) => <option key={tool}>{tool}</option>)}
    </select></label> : <label>可执行文件相对路径
      <input required value={value.target} placeholder="bin/app.exe"
        onChange={(event) => onChange({ ...value, target: event.target.value })} />
    </label>}
    {!simple && <label>工作目录绑定键
      <input required value={value.cwdBinding} onChange={(event) => onChange({ ...value, cwdBinding: event.target.value })} />
    </label>}
    <label>工作目录（相对路径）
      <input required value={value.cwd} onChange={(event) => onChange({ ...value, cwd: event.target.value })} />
    </label>
    <label>参数（每行一项）
      <textarea className="mono" rows={3} value={value.args} onChange={(event) => onChange({ ...value, args: event.target.value })} />
    </label>
  </fieldset>;
}

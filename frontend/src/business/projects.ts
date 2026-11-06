import type { Binding, Command, ProjectDocument } from "../services/models";

export interface CommandDraft {
  mode: "bound" | "runtime" | "system";
  locator: string;
  target: string;
  args: string;
  cwdBinding: string;
  cwd: string;
}

export const emptyCommand: CommandDraft = {
  mode: "bound", locator: "source", target: "", args: "", cwdBinding: "source", cwd: ".",
};

export function editCommand(command: Command): CommandDraft {
  const { executable } = command;
  return {
    mode: executable.kind === "binding" ? "bound" : executable.kind === "runtime" ? "runtime" : "system",
    locator: executable.binding ?? executable.requirementId ?? "",
    target: executable.relativePath ?? executable.tool ?? "",
    args: command.args.join("\n"),
    cwdBinding: command.cwd.binding,
    cwd: command.cwd.relativePath,
  };
}

export function buildCommand(draft: CommandDraft, existing?: Command): Command {
  const target = draft.target.trim();
  const locator = draft.locator.trim();
  if (!target || !draft.cwd.trim() || !draft.cwdBinding.trim() || (draft.mode !== "system" && !locator)) {
    throw new Error("请填写可执行项、所需绑定和工作目录。");
  }
  const relativePath = target.replaceAll("\\", "/");
  return {
    ...existing,
    executable: draft.mode === "bound" ? { kind: "binding", binding: locator, relativePath } :
      draft.mode === "runtime" ? { kind: "runtime", requirementId: locator, relativePath } :
      { kind: "system", tool: target },
    // Arguments stay separate across the Wails boundary. An internal blank line is an empty argument.
    args: draft.args === "" ? [] : draft.args.split(/\r?\n/),
    cwd: { binding: draft.cwdBinding.trim(), relativePath: draft.cwd.trim().replaceAll("\\", "/") },
    envBindings: existing?.envBindings ?? {},
  };
}

export function createProject(name: string, group: string, root: string, start: CommandDraft, stop?: CommandDraft, executables: Binding[] = []) {
  const projectId = crypto.randomUUID();
  const document: ProjectDocument = {
    schemaVersion: "1.0",
    projectId,
    name: name.trim(),
    group: group.trim() || "未分组",
    source: { kind: "provided", identity: projectId },
    requirements: {},
    files: {},
    units: {
      app: {
        resourceId: crypto.randomUUID(),
        label: name.trim(),
        dependsOn: [],
        requirementIds: [],
        kind: "process",
        control: "managed",
        start: buildCommand(start),
        readiness: { kind: "process", timeoutSeconds: 20 },
        stop: stop ? { kind: "command", command: buildCommand(stop), timeoutSeconds: 30 } :
          { kind: "manual", reason: "需要配置安全停止命令" },
      },
    },
    profiles: { local: { label: "本地运行", unitIds: ["app"], featureIds: [] } },
  };
  const bindings: Binding[] = [{ key: "source", kind: "path", localLocator: root }, ...executables];
  return { document, bindings };
}

export function bindExecutable(path: string, key: string, draft: CommandDraft) {
  const separator = Math.max(path.lastIndexOf("\\"), path.lastIndexOf("/"));
  if (separator < 0 || separator === path.length - 1) throw new Error("请选择本机可执行文件。");
  const directory = path.slice(0, separator === 2 ? 3 : separator);
  return {
    binding: { key, kind: "path", localLocator: directory } satisfies Binding,
    command: { ...draft, mode: "bound", locator: key, target: path.slice(separator + 1) } satisfies CommandDraft,
  };
}

export const runLabels: Record<string, string> = {
  stopped: "已停止", running: "运行中", starting: "启动中", stopping: "停止中",
  "stop-failed": "停止失败", interrupted: "监督已中断", unverified: "运行状态待核实", "failed-start": "启动失败",
};

export function projectError(cause: unknown) {
  const detail = String(cause);
  const messages: Record<string, string> = {
    CHECKS_NOT_READY: "环境检查还有待处理项。打开「环境」查看缺项和修复步骤。",
    AUTOMATIC_STOP_UNSUPPORTED: "缺少安全停止方式。请在「启停脚本」补齐后再启动。",
    REVISION_CONFLICT: "配置已更新。当前输入保留，请先重新加载最新配置，再核对你的修改。",
    PROJECT_MISSING: "找不到该项目。请返回项目库。",
    PROJECT_EXISTS: "该项目已登记。请打开项目库中的现有项目。",
    PLAN_STALE: "配置或本机绑定已变更，请重新生成操作计划。",
  };
  const match = Object.keys(messages).find((code) => detail.includes(code));
  return match ? messages[match] : detail;
}

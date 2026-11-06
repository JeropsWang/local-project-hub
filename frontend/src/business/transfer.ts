import type { Binding, ProjectDocument } from "../services/models";
import type { ImportPreview } from "../services/transfer";

type BindingKind = Binding["kind"];

export interface BindingRequirement {
  key: string;
  kind?: BindingKind;
}

export interface BindingDraft {
  kind?: BindingKind;
  value: string;
}

// Binding keys in the portable manifest carry no machine-specific values.
// Infer a field only when every use of the key agrees on its kind.
export function documentBindingRequirements(
  source: ProjectDocument | ImportPreview["document"],
  keys?: string[],
): BindingRequirement[] {
  // Both calls receive the same validated Go project document.
  const document = source as ImportPreview["document"];
  const kinds = new Map<string, Set<BindingKind>>();
  function add(key: string | undefined, kind: BindingKind) {
    if (!key) return;
    const existing = kinds.get(key) ?? new Set<BindingKind>();
    existing.add(kind);
    kinds.set(key, existing);
  }

  for (const file of Object.values(document.files ?? {})) add(file.path?.binding, "path");
  for (const [key, requirement] of Object.entries(document.requirements ?? {})) {
    if (requirement.location === "windows") add(key, "runtime");
  }
  for (const unit of Object.values(document.units ?? {})) {
    add(unit.binding, "resource");
    add(unit.engineBinding, "resource");
    for (const command of [unit.start, unit.command, unit.stop?.command]) {
      add(command?.cwd?.binding, "path");
      add(command?.executable?.binding, "path");
      add(command?.executable?.requirementId, "runtime");
      for (const key of Object.values(command?.envBindings ?? {})) add(key, "secret");
    }
  }

  return (keys ?? Array.from(kinds.keys()).sort()).map((key) => {
    const known = kinds.get(key);
    return { key, kind: known?.size === 1 ? known.values().next().value : undefined };
  });
}

export function collectImportBindings(
  requirements: BindingRequirement[],
  values: Record<string, BindingDraft>,
): Binding[] {
  return requirements.flatMap<Binding>(({ key, kind: inferred }) => {
    const kind = inferred ?? values[key]?.kind;
    const value = (values[key]?.value ?? "").trim();
    if (!kind || !value) return [];
    return kind === "secret" ? [{ key, kind, secretRef: value }] : [{ key, kind, localLocator: value }];
  });
}

const transferErrors: Record<string, string> = {
  BUNDLE_MANIFEST_MISSING: "这个 ZIP 缺少管理器配置清单。GitHub 源码 ZIP 需先解压，再到项目库选择「从目录添加」。",
  BUNDLE_ZIP_INVALID: "无法读取 ZIP。请检查文件是否损坏，或重新导出配置包。",
  BUNDLE_LIMIT: "配置包超出大小或文件数限制。请检查是否选择了管理器导出的 ZIP。",
  BUNDLE_MANAGER_VERSION_UNSUPPORTED: "此配置包需要更新版本的 Local Project Hub。",
  BUNDLE_INVENTORY_MISMATCH: "配置包的内容与清单不一致。请从源工作空间重新导出。",
  BUNDLE_CHANGED: "配置包在预览后发生了变化，请重新预览。",
  IMPORT_PREVIEW_EXPIRED: "预览已过期。请重新预览配置包，再确认登记。",
  IMPORT_PREVIEW_MISSING: "找不到此次预览。请重新预览配置包。",
  PROJECT_EXISTS: "工作空间已有同一项目。请在项目库打开已有项目，或在其他工作空间导入。",
  IMPORT_RESOURCE_REBIND_REQUIRED: "目标工作空间已有同标识的容器资源，请先确认资源归属，配置包不会继承控制授权。",
  EXPORT_DESTINATION_EXISTS: "所选位置已有文件。请选择新文件名后重新导出。",
  REVISION_CONFLICT: "项目配置已更新。请重新选择当前项目并预览导出。",
  WORKSPACE_REBIND_REQUIRED: "工作空间尚未确认迁移。请先在设置中确认，再继续。",
};

export function transferError(cause: unknown) {
  const detail = String(cause);
  return Object.entries(transferErrors).find(([code]) => detail.includes(code))?.[1] ?? detail;
}

export function transferWarning(code: string) {
  if (code === "SCRIPT_DYNAMIC_REFERENCES_UNVERIFIED") {
    return "脚本可能引用额外文件，迁移后请核对脚本及其依赖。";
  }
  return code;
}

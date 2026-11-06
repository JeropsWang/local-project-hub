import { Dialogs } from "@wailsio/runtime";
import { call } from "./client";
import type { Binding, ProfileRef } from "./models";
import type { Plan } from "../business/tasks";

interface PortableCommand {
  cwd?: { binding: string };
  executable?: { binding?: string; requirementId?: string };
  envBindings?: Record<string, string>;
}

export interface ImportPreview {
  id: string;
  expiresAt: string;
  document: {
    projectId: string;
    name: string;
    group?: string;
    description?: string;
    schemaVersion: string;
    profiles: Record<string, { label: string }>;
    files: Record<string, {
      availability?: string;
      bundlePath?: string;
      path?: { binding: string };
    }>;
    requirements: Record<string, { location: string }>;
    units: Record<string, {
      binding?: string;
      engineBinding?: string;
      start?: PortableCommand;
      command?: PortableCommand;
      stop?: { command?: PortableCommand };
    }>;
  };
  requiredBindings: string[] | null;
  warnings: string[] | null;
}

export function previewImport(path: string) {
  return call<ImportPreview>("PreviewImport", path);
}

export function planImport(previewId: string, bindings: Binding[]) {
  return call<Plan>("PlanImport", { previewId, bindings });
}

export function planExport(ref: ProfileRef, destination: string) {
  return call<Plan>("PlanExport", ref, destination);
}

export async function chooseBindingLocation(kind: "path" | "runtime") {
  try {
    return await Dialogs.OpenFile({
      Title: kind === "path" ? "选择本机目录" : "选择运行时可执行文件",
      CanChooseDirectories: kind === "path",
      CanChooseFiles: kind === "runtime",
      AllowsMultipleSelection: false,
      Filters: kind === "runtime" ? [{ DisplayName: "Windows 可执行文件", Pattern: "*.exe" }] : undefined,
    });
  } catch (cause) {
    if (/Dialog\.OpenFile failed[,:] error getting selection: cancelled by user$/.test(String(cause))) return "";
    throw cause;
  }
}

export async function chooseExportArchive(projectName: string) {
  const filename = projectName.replace(/[<>:"/\\|?*\u0000-\u001f]/g, "-").slice(0, 80).trim() || "project";
  try {
    const path = await Dialogs.SaveFile({
      Title: "保存项目配置包",
      Filename: `LPH-${filename}.zip`,
      Filters: [{ DisplayName: "项目配置包", Pattern: "*.zip" }],
    });
    if (!path) return "";
    return /\.zip$/i.test(path) ? path : `${path}.zip`;
  } catch (cause) {
    // The Windows dialog reports a normal dismissal as a rejected call.
    if (/Dialog\.SaveFile failed[,:] error getting selection: cancelled by user$/.test(String(cause))) return "";
    throw cause;
  }
}

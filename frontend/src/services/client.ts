import { Call, Dialogs, Events } from "@wailsio/runtime";

const service = "github.com/JeropsWang/local-project-hub/internal/desktop/services.Manager.";

export function call<T>(method: string, ...args: unknown[]): Promise<T> {
  return Call.ByName(service + method, ...args) as Promise<T>;
}

export function onExitRequested(handler: () => void) {
  return Events.On("manager:request-exit", handler);
}

async function chooseSinglePath(options: Parameters<typeof Dialogs.OpenFile>[0]): Promise<string> {
  try {
    return await Dialogs.OpenFile({ ...options, AllowsMultipleSelection: false });
  } catch (cause) {
    // Wails v3 on Windows reports dismissal as a rejected runtime call.
    if (/Dialog\.OpenFile failed[,:] error getting selection: cancelled by user$/.test(String(cause))) {
      return "";
    }
    throw cause;
  }
}

// Wails keeps selection in the native Windows dialog, including folder creation.
export function chooseWorkspaceDirectory() {
  return chooseSinglePath({
    Title: "选择项目管理工作空间",
    CanChooseDirectories: true,
    CanChooseFiles: false,
    CanCreateDirectories: true,
  });
}

export function chooseImportArchive() {
  return chooseSinglePath({
    Title: "选择要导入的项目配置包",
    CanChooseFiles: true,
    Filters: [{ DisplayName: "项目配置包", Pattern: "*.zip" }],
  });
}

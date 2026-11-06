import { Call, Dialogs, Events } from "@wailsio/runtime";

const service = "github.com/JeropsWang/local-project-hub/internal/desktop/services.Manager.";

export function call<T>(method: string, ...args: unknown[]): Promise<T> {
  return Call.ByName(service + method, ...args) as Promise<T>;
}

export function onExitRequested(handler: () => void) {
  return Events.On("manager:request-exit", handler);
}

// Wails keeps selection in the native Windows dialog, including folder creation.
export function chooseWorkspaceDirectory() {
  return Dialogs.OpenFile({
    Title: "选择项目管理工作空间",
    CanChooseDirectories: true,
    CanChooseFiles: false,
    CanCreateDirectories: true,
    AllowsMultipleSelection: false,
  });
}

export function chooseImportArchive() {
  return Dialogs.OpenFile({
    Title: "选择要导入的项目配置包",
    CanChooseFiles: true,
    AllowsMultipleSelection: false,
    Filters: [{ DisplayName: "项目配置包", Pattern: "*.zip" }],
  });
}

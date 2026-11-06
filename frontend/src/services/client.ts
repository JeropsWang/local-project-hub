import { Call, Events } from "@wailsio/runtime";

const service = "github.com/JeropsWang/local-project-hub/internal/desktop/services.Manager.";

export function call<T>(method: string, ...args: unknown[]): Promise<T> {
  return Call.ByName(service + method, ...args) as Promise<T>;
}

export function onExitRequested(handler: () => void) {
  return Events.On("manager:request-exit", handler);
}

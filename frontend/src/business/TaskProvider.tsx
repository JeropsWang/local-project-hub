import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { call, onExitRequested } from "../services/client";
import { resolveTask, submitPlan, type Plan, type TaskView } from "./tasks";

interface TaskEvent {
  seq: string;
  payload: { code: string; stepId?: string; message?: string };
}

export interface TrackedTask extends TaskView {
  events: TaskEvent[];
}

function terminal(state: string) {
  return ["failed", "cancelled", "interrupted", "succeeded"].includes(state);
}

interface TaskContextValue {
  tasks: TrackedTask[];
  submit: (plan: Plan) => Promise<TaskView>;
  exit: () => Promise<void>;
  change: (task: TaskView, action: string) => Promise<void>;
  open: boolean;
  setOpen: (open: boolean) => void;
  error: string;
  completed: number;
}

const TaskContext = createContext<TaskContextValue | null>(null);

export function useTasks() {
  const context = useContext(TaskContext);
  if (!context) throw new Error("TaskProvider is required");
  return context;
}

export function TaskProvider({ children }: { children: ReactNode }) {
  const [tasks, setTasks] = useState<TrackedTask[]>([]);
  const [open, setOpen] = useState(false);
  const [error, setError] = useState("");
  const [completed, setCompleted] = useState(0);
  const submitting = useRef(new Map<string, Promise<TaskView>>());
  const requestIDs = useRef(new Map<string, string>());
  const exitPending = useRef(false);

  const track = useCallback((view: TaskView) => {
    setTasks((previous) => [
      { ...view, events: previous.find((item) => item.id === view.id)?.events ?? [] },
      ...previous.filter((item) => item.id !== view.id),
    ]);
    if (terminal(view.state) || view.state === "waiting-user") setCompleted((value) => value + 1);
  }, []);

  const exit = useCallback(async () => {
    if (exitPending.current) return;
    exitPending.current = true;
    setOpen(true);
    setError("");
    try {
      track(await call<TaskView>("RequestExit"));
    } catch (cause) {
      setError(String(cause));
    } finally {
      exitPending.current = false;
    }
  }, [track]);

  useEffect(() => onExitRequested(() => void exit()), [exit]);

  // Poll by identity. A request from an earlier render cannot replace a task
  // the user just cancelled or resumed.
  useEffect(() => {
    const pending = tasks.filter((task) => !terminal(task.state) || task.events.at(-1)?.seq !== task.lastEventSeq);
    if (!pending.length) return;
    let disposed = false;
    const timer = window.setTimeout(async () => {
      let failed = false;
      const results = await Promise.all(pending.map(async (task) => {
        try {
          const next = terminal(task.state) ? task : await call<TaskView>("Task", task.id);
          const cursor = task.events.at(-1)?.seq ?? "";
          const events = cursor === next.lastEventSeq ? [] : await call<TaskEvent[]>("TaskEvents", task.id, cursor);
          return { ...next, projectId: task.projectId, events: [...task.events, ...(events ?? [])] };
        } catch {
          failed = true;
          return task;
        }
      }));
      if (disposed) return;
      if (failed) setError("暂时无法刷新任务状态，正在重试。");
      else setError((current) => current === "暂时无法刷新任务状态，正在重试。" ? "" : current);
      const updated = tasks.map((task) => results.find((item) => item.id === task.id) ?? task);
      if (updated.some((task, index) => task.state !== tasks[index].state &&
        (terminal(task.state) || task.state === "waiting-user"))) {
        setCompleted((value) => value + 1);
      }
      setTasks(updated);
    }, 600);
    return () => {
      disposed = true;
      window.clearTimeout(timer);
    };
  }, [tasks]);

  function submit(plan: Plan) {
    const pending = submitting.current.get(plan.id);
    if (pending) return pending;
    const id = requestIDs.current.get(plan.id) ?? crypto.randomUUID();
    requestIDs.current.set(plan.id, id);
    const promise = submitPlan(plan, id).then((result) => {
      const view = { ...result, projectId: plan.projectId };
      track(view);
      setOpen(true);
      return view;
    }).finally(() => submitting.current.delete(plan.id));
    submitting.current.set(plan.id, promise);
    return promise;
  }

  async function change(task: TaskView, action: string) {
    setError("");
    try {
      const next = action === "cancel"
        ? await call<TaskView>("Cancel", task.id)
        : await resolveTask(task, action);
      track({ ...next, projectId: task.projectId });
    } catch (cause) {
      setError(String(cause));
    }
  }

  return <TaskContext.Provider value={{ tasks, submit, exit, change, open, setOpen, error, completed }}>
    {children}
  </TaskContext.Provider>;
}

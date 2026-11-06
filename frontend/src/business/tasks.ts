import { call } from "../services/client";

export interface TaskView {
  id: string;
  kind: string;
  state: string;
  cancelable: boolean;
  lastEventSeq: string;
  error?: { code: string; message: string };
}

export interface Plan {
  id: string;
  fingerprint: string;
  steps: { id: string; action: string; effect: string }[];
  warnings?: string[];
  requiredBindings?: string[];
}

export function submitPlan(plan: Plan, requestId: string) {
  return call<TaskView>("Submit", {
    planId: plan.id,
    fingerprint: plan.fingerprint,
    requestId,
  });
}

export function resolveTask(task: TaskView, action: string) {
  return call<TaskView>("Resolve", {
    taskId: task.id,
    expectedEventSeq: task.lastEventSeq,
    action,
  });
}

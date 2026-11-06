import { useState } from "react";
import { useTasks } from "../business/TaskProvider";
import styles from "../css/Shell.module.css";

const kinds: Record<string, string> = {
  prepare: "准备环境", start: "启动项目", stop: "停止项目", import: "导入配置", export: "导出配置", exit: "安全退出",
};
const states: Record<string, string> = {
  queued: "排队中", running: "执行中", "waiting-user": "等待处理", "cancel-requested": "正在取消",
  succeeded: "成功", failed: "失败", cancelled: "已取消", interrupted: "已中断",
};

export function TaskPanel() {
  const { tasks, change, setOpen, error } = useTasks();
  const [pending, setPending] = useState("");
  async function act(task: (typeof tasks)[number], action: string) {
    setPending(task.id);
    try {
      await change(task, action);
    } finally {
      setPending("");
    }
  }

  return <aside className={styles.tasks} aria-label="任务面板">
    <div className="toolbar">
      <h2>任务</h2>
      <button autoFocus className="secondary" onClick={() => setOpen(false)}>收起</button>
    </div>
    {error && <p role="alert">{error}</p>}
    {tasks.length === 0 && <p>本次会话暂无任务。</p>}
    {tasks.map((task) => <section className={styles.task} key={task.id}>
      <strong>{kinds[task.kind] ?? task.kind}</strong>
      <span className="badge" data-state={task.state}>{states[task.state] ?? task.state}</span>
      {task.error && <p>{task.error.message || task.error.code}</p>}
      {task.events.length > 0 && <details>
        <summary>执行记录</summary>
        <ol>{task.events.map((event) => <li key={event.seq}>
          {event.payload.message || event.payload.stepId || event.payload.code}
        </li>)}</ol>
      </details>}
      <div className="actions">
        {task.cancelable && task.state !== "cancel-requested" && <button className="secondary" disabled={!!pending}
          onClick={() => void act(task, "cancel")}>取消任务</button>}
        {task.state === "waiting-user" && Object.entries(task.kind === "exit"
          ? { retry: "重试", "continue-wait": "继续等待", "abort-exit": "取消退出" }
          : { retry: "重试", "continue-wait": "继续等待" }).map(([action, label]) =>
          <button className="secondary" disabled={!!pending} key={action} onClick={() => void act(task, action)}>{label}</button>)}
      </div>
    </section>)}
  </aside>;
}

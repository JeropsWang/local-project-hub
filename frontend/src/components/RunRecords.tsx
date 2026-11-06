import { useEffect, useState } from "react";
import { call } from "../services/client";
import { runLabels } from "../business/projects";
import styles from "../css/Projects.module.css";

interface Run {
  id: string;
  profileId: string;
  revision: number;
  state: string;
  createdAt: string;
  failureCode?: string;
  units: { id: string; label: string; kind: string; logsAvailable: boolean }[];
}
interface LogPage {
  entries: { seq: string; stream: string; text: string }[];
  next: string;
  gap: boolean;
}

export function RunRecords({ projectId, refresh }: { projectId: string; refresh: number }) {
  const [runs, setRuns] = useState<Run[]>();
  const [selected, setSelected] = useState<{ run: string; unit: string }>();
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    let disposed = false;
    call<Run[]>("Runs", projectId).then((items) => {
      if (!disposed) { setRuns(items ?? []); setError(""); }
    }, (cause) => {
      if (!disposed) setError(String(cause));
    });
    return () => { disposed = true; };
  }, [projectId, refresh, retry]);

  return <>
    <h2>运行记录</h2>
    <button className="secondary" onClick={() => setRetry((value) => value + 1)}>刷新记录</button>
    {error && <p role="alert">{error}</p>}
    {!runs && !error && <p role="status">正在读取运行记录…</p>}
    {runs?.length === 0 && <p>暂无运行记录。</p>}
    {runs?.map((run) => <section className="surface" key={run.id}>
      <h3>{runLabels[run.state] ?? run.state} · 版本 {run.revision}</h3>
      <p>{run.profileId} · {new Date(run.createdAt).toLocaleString()}{run.failureCode ? ` · ${run.failureCode}` : ""}</p>
      <div className="actions">{run.units.filter((unit) => unit.logsAvailable).map((unit) =>
        <button className="secondary" key={unit.id} onClick={() => setSelected({ run: run.id, unit: unit.id })}>{unit.label} 日志</button>)}</div>
      {!run.units.some((unit) => unit.logsAvailable) && <p>此运行暂无可读取的进程日志。</p>}
      {selected?.run === run.id && <ProcessLog key={selected.unit} projectId={projectId} runId={run.id} unitId={selected.unit} />}
    </section>)}
  </>;
}

function ProcessLog({ projectId, runId, unitId }: { projectId: string; runId: string; unitId: string }) {
  const [entries, setEntries] = useState<LogPage["entries"]>([]);
  const [cursor, setCursor] = useState("");
  const [gap, setGap] = useState(false);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let disposed = false;
    let timer: number;
    let after = "";
    async function load() {
      try {
        const page = await call<LogPage>("ReadLog", projectId, runId, unitId, after, 200);
        if (disposed) return;
        after = page.next || after;
        setCursor(after);
        setGap((current) => current || page.gap);
        setEntries((current) => [...(page.gap ? [] : current), ...(page.entries ?? [])].slice(-2000));
        setError("");
        timer = window.setTimeout(() => void load(), 900);
      } catch (cause) {
        if (!disposed) setError(String(cause));
      }
    }
    setEntries([]);
    setCursor("");
    setGap(false);
    void load();
    return () => { disposed = true; window.clearTimeout(timer); };
  }, [projectId, runId, unitId, retry]);

  return <>
    {gap && <p>较早的日志已轮转，以下从现有记录开始。</p>}
    {error && <p role="alert">{error} <button className="secondary" onClick={() => setRetry((value) => value + 1)}>重试</button></p>}
    <pre className={styles.log} aria-label="进程日志" tabIndex={0}>
      {entries.length ? entries.map((entry) => `[${entry.stream}] ${entry.text}`).join("\n") : cursor ? "没有更多日志" : "等待进程日志…"}
    </pre>
  </>;
}

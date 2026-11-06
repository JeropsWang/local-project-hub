import { useEffect, useState } from "react";
import { containerHref, projectHref } from "../router/routes";
import { call } from "../services/client";
import type { ContainerView, ProjectView } from "../services/models";
import styles from "../css/DockerPage.module.css";

const relations = {
  all: "全部容器",
  dedicated: "专用",
  shared: "共享",
  reference: "仅引用",
  untracked: "未关联",
} as const;
type Filter = keyof typeof relations;

const states: Record<string, string> = {
  running: "运行中",
  exited: "已退出",
  created: "已创建",
  restarting: "正在重启",
  paused: "已暂停",
  dead: "异常退出",
  removing: "正在移除",
};

function relation(container: ContainerView): Exclude<Filter, "all"> {
  if (container.control === "untracked" || !container.control) return "untracked";
  if (container.control === "reference") return "reference";
  return container.shared ? "shared" : "dedicated";
}

function statusLabel(status: string) {
  return states[status] ?? (status || "未知");
}

interface DockerPageProps {
  projects: ProjectView[];
  selectedID?: string;
}

export function DockerPage({ projects, selectedID }: DockerPageProps) {
  const [containers, setContainers] = useState<ContainerView[]>([]);
  const [filter, setFilter] = useState<Filter>("all");
  const [revision, setRevision] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const projectNames = new Map(projects.map(({ document }) => [document.projectId, document.name]));

  // Refresh does not let a delayed inventory response replace a newer view.
  useEffect(() => {
    let disposed = false;
    setLoading(true);
    setError("");
    void call<ContainerView[]>("Containers").then((items) => {
      if (disposed) return;
      setContainers([...(items ?? [])].sort((a, b) =>
        a.name.localeCompare(b.name, "zh-CN") || a.id.localeCompare(b.id)));
    }).catch((cause) => {
      if (!disposed) setError(String(cause));
    }).finally(() => {
      if (!disposed) setLoading(false);
    });
    return () => { disposed = true; };
  }, [revision]);

  const selected = containers.find((container) => container.id === selectedID);
  const visible = containers.filter((container) => filter === "all" ||
    (filter === "shared" ? container.shared : relation(container) === filter));

  return (
    <section className={styles.page} aria-labelledby="docker-heading">
      <div className="toolbar">
        <div>
          <h1 id="docker-heading">Docker</h1>
          <p>查看本机容器、归属和使用它们的项目。</p>
        </div>
        <button type="button" disabled={loading} onClick={() => setRevision((value) => value + 1)}>
          {loading ? "正在读取" : error ? "重试连接" : "刷新清单"}
        </button>
      </div>
      {error && (
        <div className={styles.notice} role="alert">
          <strong>无法读取 Docker 清单</strong>
          <p>请确认本机 Docker 引擎已启动，再重试。{containers.length > 0 && "当前显示上一次读取的结果。"}</p>
          <details><summary>查看原因</summary><code>{error}</code></details>
        </div>
      )}
      {loading && <p role="status">正在读取容器清单…</p>}
      {containers.length > 0 && (
        <div className={styles.layout}>
          <div className={styles.inventory}>
            <label className={styles.filter}>
              归属
              <select value={filter} onChange={(event) => setFilter(event.target.value as Filter)}>
                {Object.entries(relations).map(([key, label]) => (
                  <option key={key} value={key}>{label}</option>
                ))}
              </select>
            </label>
            <p className={styles.count}>{visible.length} 个容器</p>
            {visible.length === 0 ? (
              <p className={styles.empty}>这个筛选下没有容器。</p>
            ) : (
              <table className={styles.table}>
                <thead>
                  <tr><th scope="col">容器 / 镜像</th><th scope="col">状态</th><th scope="col">归属 / 使用者</th></tr>
                </thead>
                <tbody>
                  {visible.map((container) => (
                    <tr key={container.id} data-selected={container.id === selectedID}>
                      <td>
                        <a href={containerHref(container.id)} aria-current={container.id === selectedID ? "true" : undefined}>
                          {container.name || container.id.slice(0, 12)}
                        </a>
                        <small className={styles.image} title={container.image}>{container.image}</small>
                        {container.composeService && <small>Compose 服务 {container.composeService}</small>}
                      </td>
                      <td><span className="badge" data-state={container.status}>{statusLabel(container.status)}</span></td>
                      <td>
                        {relations[relation(container)]}
                        {container.shared && container.control === "reference" && <small>共享资源</small>}
                        <small>{container.projectIds?.map((id) => projectNames.get(id) ?? id).join("、") || "无记录"}</small>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
          {selected && (
            <section className={styles.details} aria-labelledby="container-details-heading">
              <div className={styles.detailHeading}>
                <h2 id="container-details-heading">{selected.name || selected.id.slice(0, 12)}</h2>
                <a href="#/docker">返回清单</a>
              </div>
              <dl>
                <dt>状态</dt><dd>{statusLabel(selected.status)}</dd>
                <dt>实例 ID</dt><dd><code>{selected.id}</code></dd>
                <dt>镜像</dt><dd>{selected.image || "未知"}</dd>
                <dt>Compose</dt><dd>{[selected.composeProject, selected.composeService].filter(Boolean).join(" / ") || "无记录"}</dd>
                <dt>控制权</dt><dd>{selected.control === "managed" ? "由管理器控制" : selected.control === "reference" ? "仅引用" : "未关联"}</dd>
                <dt>共享</dt><dd>{selected.shared ? "共享资源" : "未标记共享"}</dd>
              </dl>
              <h3>使用项目</h3>
              {selected.projectIds?.length ? (
                <ul>
                  {selected.projectIds.map((id) => <li key={id}><a href={projectHref(id)}>{projectNames.get(id) ?? id}</a></li>)}
                </ul>
              ) : <p>当前没有已记录的项目使用此容器。</p>}
            </section>
          )}
        </div>
      )}
      {!loading && !error && containers.length === 0 && <p className={styles.empty}>当前 Docker 引擎没有容器。</p>}
      {!loading && !error && selectedID && !selected && (
        <p role="status">此容器已不在当前清单中。<a href="#/docker">返回全部容器</a></p>
      )}
    </section>
  );
}

import { useEffect, useState, type PointerEvent } from "react";
import type { ProjectView } from "../services/models";
import { call } from "../services/client";
import { runLabels } from "../business/projects";
import { projectHref } from "../router/routes";
import styles from "../css/Projects.module.css";

function Cover({ project }: { project: ProjectView }) {
  const [cover, setCover] = useState("");
  useEffect(() => {
    if (!project.document.appearance?.coverFileId) return;
    let disposed = false;
    call<string>("ProjectCover", project.document.projectId).then((url) => {
      if (!disposed) setCover(url);
    }, () => {});
    return () => { disposed = true; };
  }, [project.document.projectId, project.revision, project.document.appearance?.coverFileId]);

  function move(event: PointerEvent<HTMLDivElement>) {
    if (event.pointerType !== "mouse") return;
    const rect = event.currentTarget.getBoundingClientRect();
    event.currentTarget.style.setProperty("--cover-x", `${((event.clientX - rect.left) / rect.width - .5) * 8}px`);
    event.currentTarget.style.setProperty("--cover-y", `${((event.clientY - rect.top) / rect.height - .5) * 8}px`);
  }
  return <div className={styles.cover} onPointerMove={move} onPointerLeave={(event) => {
    event.currentTarget.style.removeProperty("--cover-x");
    event.currentTarget.style.removeProperty("--cover-y");
  }}>
    {cover ? <img src={cover} alt="" loading="lazy" /> : <span aria-hidden="true">{Array.from(project.document.name)[0]}</span>}
  </div>;
}

export function LibraryPage({ projects, loading, ready, checks }: {
  projects: ProjectView[];
  loading: boolean;
  ready: boolean;
  checks: Record<string, string>;
}) {
  const [search, setSearch] = useState("");
  const [group, setGroup] = useState("");
  const [covers, setCovers] = useState(() => localStorage.getItem("library-view") === "covers");
  const groups = [...new Set(projects.map(({ document }) => document.group || "未分组"))].sort((a, b) => a.localeCompare(b, "zh-CN"));
  const visible = projects.filter(({ document }) => (!group || (document.group || "未分组") === group) &&
    document.name.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase()));

  function mode(next: boolean) {
    setCovers(next);
    localStorage.setItem("library-view", next ? "covers" : "list");
  }

  return <>
    <div className="toolbar">
      <div><h1>项目库</h1><p>从项目查看运行状态、环境与依赖。</p></div>
      {ready && <a className="button" href="#/projects/new">添加项目</a>}
    </div>
    {loading ? <p role="status">正在读取项目…</p> : projects.length === 0 ?
      <section className={styles.empty}>
        <h2>添加你的第一个项目</h2>
        <p>选一个已解压的源码目录，确认启动与停止方式。已有配置包也可以直接导入。</p>
        <a href="#/transfer">前往配置迁移</a>
      </section> : <>
        <div className={styles.filters}>
          <label>搜索项目<input type="search" placeholder="输入项目名称" value={search} onChange={(event) => setSearch(event.target.value)} /></label>
          <label>分组<select value={group} onChange={(event) => setGroup(event.target.value)}>
            <option value="">全部分组</option>{groups.map((name) => <option key={name}>{name}</option>)}
          </select></label>
          <div className="actions" aria-label="展示方式">
            <button className={covers ? "secondary" : ""} aria-pressed={!covers} onClick={() => mode(false)}>列表</button>
            <button className={covers ? "" : "secondary"} aria-pressed={covers} onClick={() => mode(true)}>封面</button>
          </div>
        </div>
        {visible.length === 0 && <p>没有匹配的项目。请调整名称或分组。</p>}
        {groups.map((name) => {
          const items = visible.filter(({ document }) => (document.group || "未分组") === name);
          if (!items.length) return null;
          return <section key={name} className={styles.group}>
            <h2>{name} <small>{items.length}</small></h2>
            <ul className={covers ? styles.gallery : styles.list}>
              {items.map((project) => {
                const [profile, configuration] = Object.entries(project.document.profiles)[0] ?? [];
                const check = checks[`${project.document.projectId}:${project.revision}:${profile}`];
                return <li key={project.document.projectId}>
                  <a href={projectHref(project.document.projectId)} className={styles.row}>
                    {covers && <Cover key={project.revision} project={project} />}
                    <strong>{project.document.name}</strong>
                    <span className="badge" title={configuration?.label} data-state={check === "ready" ? "ready" : check ? "blocked" : undefined}>
                      {check === "ready" ? "环境就绪" : check ? "环境需处理" : "环境未检测"}
                    </span>
                    <span className="badge" data-state={project.runSummary}>{runLabels[project.runSummary] ?? project.runSummary}</span>
                  </a>
                </li>;
              })}
            </ul>
          </section>;
        })}
      </>}
  </>;
}

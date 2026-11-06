import { useState } from "react";
import { call } from "../services/client";

interface Props {
  document?: { projectId: string };
  revision?: number;
  disabled: boolean;
  perform: (action: () => Promise<void>) => Promise<void>;
  refresh: () => Promise<void>;
}

export function ConfigurationEditor({ document, revision, disabled, perform, refresh }: Props) {
  const [source, setSource] = useState(() => JSON.stringify(document ?? {}, null, 2));
  const [bindings, setBindings] = useState("[]");

  return <details>
    <summary>{document ? "高级 JSON 配置" : "从项目 JSON 登记"}</summary>
    <label>项目 JSON<textarea rows={12} spellCheck={false} value={source} onChange={(event) => setSource(event.target.value)} /></label>
    <label>新增或更新的本机绑定<textarea rows={4} spellCheck={false} value={bindings} onChange={(event) => setBindings(event.target.value)} /></label>
    <p>使用 key、kind、localLocator 填写路径绑定。配置保存后，正在运行的项目仍使用启动时的版本。</p>
    <button disabled={disabled} onClick={() => void perform(async () => {
      const edited = JSON.parse(source);
      if (document && edited.projectId !== document.projectId) throw new Error("已登记项目的 ID 不能更改");
      const request = { document: edited, bindings: JSON.parse(bindings), expectedRevision: revision };
      await call(document ? "Save" : "Register", request);
      await refresh();
    })}>{document ? "保存新版本" : "登记项目"}</button>
  </details>;
}

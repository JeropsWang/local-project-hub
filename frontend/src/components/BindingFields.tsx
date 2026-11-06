import { useState } from "react";
import type { Binding } from "../services/models";
import type { BindingDraft, BindingRequirement } from "../business/transfer";
import { chooseBindingLocation } from "../services/transfer";
import styles from "../css/BindingFields.module.css";

const kindLabels: Record<Binding["kind"], string> = {
  path: "文件夹",
  runtime: "运行时可执行文件",
  resource: "本机资源引用",
  secret: "本机凭据引用",
};

const placeholders: Record<Binding["kind"], string> = {
  path: "例如 D:\\Projects\\example",
  runtime: "例如 C:\\Tools\\node.exe",
  resource: "填写已配置的本机资源标识",
  secret: "填写引用标识",
};

interface BindingFieldsProps {
  requirements: BindingRequirement[];
  values: Record<string, BindingDraft>;
  onChange: (key: string, draft: BindingDraft) => void;
  disabled?: boolean;
}

export function BindingFields({ requirements, values, onChange, disabled }: BindingFieldsProps) {
  const [browsing, setBrowsing] = useState("");
  const [error, setError] = useState("");

  async function browse(key: string, kind: "path" | "runtime") {
    setBrowsing(key);
    setError("");
    try {
      const value = await chooseBindingLocation(kind);
      if (value) onChange(key, { kind, value });
    } catch (cause) {
      setError(String(cause));
    } finally {
      setBrowsing("");
    }
  }

  return (
    <fieldset className={styles.fields} disabled={disabled || !!browsing}>
      <legend>本机绑定</legend>
      <p>可以先填写已知的本机位置，留空的项目可在登记后补齐。</p>
      {requirements.map(({ key, kind: inferred }) => {
        const draft = values[key];
        const kind = inferred ?? draft?.kind;
        return (
          <div className={styles.field} key={key}>
            <strong><code>{key}</code></strong>
            {!inferred && (
              <label>
                绑定类型
                <select value={kind ?? ""} onChange={(event) => onChange(key, {
                  kind: (event.target.value || undefined) as Binding["kind"] | undefined,
                  value: "",
                })}>
                  <option value="">选择类型</option>
                  {Object.entries(kindLabels).map(([value, label]) => (
                    <option key={value} value={value}>{label}</option>
                  ))}
                </select>
              </label>
            )}
            {kind && (
              <div className={styles.entry}>
                <label>
                  {kindLabels[kind]}
                  <input
                    value={draft?.value ?? ""}
                    placeholder={placeholders[kind]}
                    autoComplete="off"
                    spellCheck={false}
                    onChange={(event) => onChange(key, { kind, value: event.target.value })}
                  />
                </label>
                {(kind === "path" || kind === "runtime") && (
                  <button className="secondary" type="button" onClick={() => void browse(key, kind)}>
                    {browsing === key ? "正在浏览" : "浏览"}
                  </button>
                )}
              </div>
            )}
            {kind === "secret" && <small>此处只接收已配置的凭据引用，请勿输入密码或令牌。</small>}
          </div>
        );
      })}
      {error && <p role="alert">无法选择位置。{error}</p>}
    </fieldset>
  );
}

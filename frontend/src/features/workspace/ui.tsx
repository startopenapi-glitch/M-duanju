import { PageHeader as SharedHeader } from "../../components/ui/PageHeader";
import { useId, useRef, type ReactNode } from "react";
import { useFocusScope } from "../../layout/useFocusScope";
export type Page<T> = {
  items: T[];
  total: number;
  page: number;
  pageSize: number;
};
export const day = (v?: string) =>
  v ? new Date(v).toLocaleString("zh-CN", { timeZone: "Asia/Shanghai" }) : "—";
export const money = (cents?: number) =>
  typeof cents === "number" ? `¥${(cents / 100).toFixed(2)}` : "—";
export function PageHeader({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children?: ReactNode;
}) {
  return (
    <SharedHeader title={title} description={description} actions={children} />
  );
}

export function State({
  loading,
  error,
  retry,
  empty,
  emptyLabel = "暂无记录",
}: {
  loading?: boolean;
  error?: string;
  retry: () => void;
  empty?: boolean;
  emptyLabel?: string;
}) {
  if (error)
    return (
      <div className="ws-state ws-error" role="alert">
        <b>加载失败</b>
        <p>{error}</p>
        <button className="btn" onClick={retry}>
          重试
        </button>
      </div>
    );
  if (loading)
    return (
      <div className="ws-state" role="status">
        <span className="ws-spinner" />
        正在加载…
      </div>
    );
  if (empty)
    return (
      <div className="ws-state">
        <span className="ws-empty-icon" aria-hidden>
          ◇
        </span>
        <b>{emptyLabel}</b>
        <p>当前条件下没有可显示的数据。</p>
      </div>
    );
  return null;
}
export function Pager({
  page,
  total,
  pageSize = 20,
  onPage,
  disabled = false,
}: {
  page: number;
  total: number;
  pageSize?: number;
  onPage: (n: number) => void;
  disabled?: boolean;
}) {
  const pages = Math.max(1, Math.ceil(total / pageSize));
  return (
    <nav className="ws-pager" aria-label="分页">
      <span>
        共 {total} 条 · 第 {page} / {pages} 页
      </span>
      <div>
        <button
          className="btn"
          disabled={disabled || page <= 1}
          onClick={() => onPage(page - 1)}
        >
          上一页
        </button>
        <button
          className="btn"
          disabled={disabled || page >= pages}
          onClick={() => onPage(page + 1)}
        >
          下一页
        </button>
      </div>
    </nav>
  );
}
export function Filters({
  keyword,
  onKeyword,
  status,
  onStatus,
  label = "状态",
  statuses = [],
}: {
  keyword: string;
  onKeyword: (s: string) => void;
  status?: string;
  onStatus?: (s: string) => void;
  label?: string;
  statuses?: [string, string][];
}) {
  return (
    <div className="ws-filters">
      <label className="ws-search">
        <span>搜索</span>
        <input
          className="input"
          type="search"
          value={keyword}
          placeholder="搜索 ID、邮箱或关键词"
          onChange={(e) => onKeyword(e.target.value)}
        />
      </label>
      {onStatus && (
        <label>
          <span>{label}</span>
          <select
            className="input"
            value={status}
            onChange={(e) => onStatus(e.target.value)}
          >
            <option value="">全部状态</option>
            {statuses.map(([value, text]) => (
              <option key={value} value={value}>
                {text}
              </option>
            ))}
          </select>
        </label>
      )}
    </div>
  );
}
export function Feedback({
  action,
}: {
  action: { busy: boolean; error: string; notice: string };
}) {
  return (
    <>
      {action.error && (
        <p className="ws-error note" role="alert">
          {action.error}
        </p>
      )}
      {action.notice && (
        <p className="note note-info" role="status">
          {action.notice}
        </p>
      )}
    </>
  );
}
export function Detail({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null),
    id = useId();
  useFocusScope(ref, true, onClose);
  return (
    <div className="ws-overlay" onClick={onClose}>
      <section
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-labelledby={id}
        tabIndex={-1}
        className="ws-detail"
        onClick={(e) => e.stopPropagation()}
      >
        <header>
          <div>
            <span className="ws-eyebrow">记录详情</span>
            <h2 id={id}>{title}</h2>
          </div>
          <button className="btn" onClick={onClose} aria-label="关闭详情">
            关闭 ×
          </button>
        </header>
        <div className="ws-detail-body">{children}</div>
      </section>
    </div>
  );
}
export function Facts({ items }: { items: [string, ReactNode][] }) {
  return (
    <dl className="ws-facts">
      {items.map(([label, value]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd>{value ?? "—"}</dd>
        </div>
      ))}
    </dl>
  );
}
export type Audit = {
  id: string;
  userId?: string;
  action: string;
  projectId?: string;
  fromStatus?: string;
  toStatus?: string;
  note?: string;
  createdAt: string;
};
export function Timeline({ items }: { items: Audit[] }) {
  return (
    <section className="ws-section">
      <h3>事件时间线</h3>
      {!items.length ? (
        <p className="ws-muted">暂无事件</p>
      ) : (
        <ol className="ws-timeline">
          {items.map((e) => (
            <li key={e.id}>
              <span>{day(e.createdAt)}</span>
              <b>{e.action}</b>
              <p>
                {e.fromStatus && `${e.fromStatus} → ${e.toStatus || "—"} `}
                {e.note}
              </p>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
export function Table({
  head,
  children,
}: {
  head: string[];
  children: ReactNode;
}) {
  return (
    <div className="ws-table-wrap">
      <table className="ws-table">
        <thead>
          <tr>
            {head.map((h) => (
              <th key={h}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}
export function Cell({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return <td data-label={label}>{children}</td>;
}

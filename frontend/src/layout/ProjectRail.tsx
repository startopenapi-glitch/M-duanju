import { AccountMenu } from "./AccountMenu";
import { Link } from "react-router-dom";
import { useProjects } from "../lib/projects";
import { useNavigate, useParams } from "react-router-dom";
import { api, type Project } from "../lib/api";
import { useAuth } from "../App";
import { clearToken } from "../lib/api";
import { STATUS_LABEL } from "../lib/format";

function dotCls(s: string) {
  if (s === "COMPLETED") return "rail-dot is-done";
  if (s === "FAILED" || s === "BUDGET_EXCEEDED") return "rail-dot is-bad";
  if (s === "DRAFT" || s === "SCRIPT_REVIEW") return "rail-dot is-draft";
  return "rail-dot is-run";
}

export function ProjectRail({
  reloadKey,
  onNew,
  onNavigate = () => {},
}: {
  reloadKey: number;
  onNew: () => void;
  onNavigate?: () => void;
}) {
  const { user, setUser } = useAuth();
  const { id } = useParams();
  const nav = useNavigate();
  const { items, total, loading, error, retry, more } = useProjects(
    user?.id || "",
    reloadKey,
  );
  const go = (path: string) => {
    nav(path);
    onNavigate();
  };
  const logout = () => {
    clearToken();
    setUser(null);
    go("/login");
  };

  return (
    <aside className="app-sidebar" aria-label="项目导航">
      <div className="app-brand">
        <span className="app-brand-mark">剧</span>
        <div>
          <b>短剧工厂</b>
          <small>AI 一键成片</small>
        </div>
      </div>

      <button className="rail-new" onClick={onNew}>
        ＋ 新建短片
      </button>

      <Link to="/" onClick={onNavigate} className="app-nav-item">
        我的项目
      </Link>
      <div className="rail-label">最近项目</div>
      <nav className="rail-list">
        {error && (
          <div role="alert">
            加载失败：{error}
            <button className="btn" onClick={retry}>
              重试
            </button>
          </div>
        )}
        {loading && <div className="rail-empty">载入中…</div>}
        {!loading && !error && items.length === 0 && (
          <div className="rail-empty">还没有项目</div>
        )}
        {items.map((p) => (
          <button
            key={p.id}
            onClick={() => go(`/project/${p.id}`)}
            className={"rail-item" + (id === p.id ? " is-active" : "")}
          >
            <span className={dotCls(p.status)} />
            <span className="rail-title" title={p.title}>
              {p.title}
            </span>
            <span className="rail-sub">
              {STATUS_LABEL[p.status] || p.status}
            </span>
          </button>
        ))}
        {items.length < total && (
          <button className="btn" disabled={loading || !!error} onClick={more}>
            加载更多项目
          </button>
        )}
      </nav>

      <AccountMenu onNavigate={onNavigate} />
    </aside>
  );
}

import { useEffect, useRef, useState } from "react";
import { useProjects } from "../lib/projects";
import { useAuth } from "../App";
import { Link, useSearchParams } from "react-router-dom";
import { useNewProject } from "../layout/new-project";
import { STATUS_LABEL, tagCls, toast } from "../lib/format";
import { api, ApiError, errorMessage } from "../lib/api";
import { projectsChanged } from "../lib/projects";
import { PageHeader } from "../components/ui/PageHeader";
export default function Home() {
  const openNew = useNewProject(),
    { user } = useAuth(),
    [q, setQ] = useSearchParams();
  const filter = { q: q.get("q") || "", status: q.get("status") || "" };
  const { items, loading, error, retry, more, total } = useProjects(
    user?.id || "",
    0,
    filter,
  );
  const [deleting, setDeleting] = useState("");
  const scrollKey = `drama-project-scroll:${user?.id}:${q.toString()}`,
    restored = useRef(false);
  useEffect(() => {
    if (!loading && !restored.current) {
      restored.current = true;
      window.scrollTo?.(0, Number(sessionStorage.getItem(scrollKey) || 0));
    }
  }, [loading, scrollKey]);
  const removeProject = async (id: string, title: string) => {
    if (!window.confirm(`确定删除「${title}」？\n项目会从历史中移除；已产生的结算和扣费凭证仍会保留。`)) return;
    setDeleting(id);
    try {
      try {
        await api.del(`/projects/${id}`);
      } catch (e) {
        if (!(e instanceof ApiError) || e.status !== 409) throw e;
        if (!window.confirm("该项目包含未完成或未知上游任务。\n强制删除只会从历史中隐藏，不会取消上游任务、不会退回积分；确定继续？")) {
          return;
        }
        await api.del(`/projects/${id}?force=true`);
        toast("未完成项目已从历史中强制删除，财务与上游记录仍保留", "ok");
        projectsChanged();
        retry();
        return;
      }
      projectsChanged();
      retry();
      toast("项目已从历史中删除", "ok");
    } catch (e) {
      toast(errorMessage(e), "err");
    } finally {
      setDeleting("");
    }
  };
  const change = (key: string, value: string) => {
    restored.current = true;
    setQ(
      (prev) => {
        const n = new URLSearchParams(prev);
        value ? n.set(key, value) : n.delete(key);
        return n;
      },
      { replace: true },
    );
  };
  return (
    <div className="space-y-6">
      <PageHeader
        title="我的项目"
        description="从一个想法到一部成片，在这里继续你的创作。"
        actions={
          <button className="btn btn-primary" onClick={openNew}>
            ＋ 新建短片
          </button>
        }
      />
      <div className="ws-filters">
        <label>
          <span>搜索项目</span>
          <input
            className="input"
            type="search"
            value={filter.q}
            maxLength={100}
            onChange={(e) => change("q", e.target.value)}
            placeholder="项目名称或创作想法"
          />
        </label>
        <label>
          <span>制作状态</span>
          <select
            className="input"
            value={filter.status}
            onChange={(e) => change("status", e.target.value)}
          >
            <option value="">全部状态</option>
            {Object.entries(STATUS_LABEL).map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </label>
      </div>
      {error && (
        <div className="note note-err" role="alert">
          项目加载失败：{error}
          <button className="btn" onClick={retry}>
            重试
          </button>
        </div>
      )}
      {loading && <p role="status">加载项目…</p>}
      {!loading && !error && !items.length && (
        <section className="card ws-state">
          <h2>
            {filter.q || filter.status
              ? "没有符合条件的项目"
              : "创建你的第一部短片"}
          </h2>
          <p>写下一个想法，系统会自动推进制作。</p>
          <button className="btn btn-primary" onClick={openNew}>
            新建短片
          </button>
        </section>
      )}
      <div className="project-list">
        {items.map((p) => (
          <div key={p.id} className="card project-row">
            <Link
              to={`/project/${p.id}`}
              onClick={() =>
                sessionStorage.setItem(scrollKey, String(window.scrollY))
              }
              className="project-row-link"
            >
              <div>
                <h2>{p.title}</h2>
                <p>{p.idea}</p>
              </div>
            </Link>
            <div className="project-row-actions">
              <span className={`tag ${tagCls(p.status)}`}>
                {STATUS_LABEL[p.status] || p.status}
              </span>
              <button
                className="btn btn-danger project-delete"
                type="button"
                disabled={deleting === p.id}
                onClick={() => removeProject(p.id, p.title)}
                aria-label={`删除项目 ${p.title}`}
              >
                {deleting === p.id ? "删除中…" : "删除"}
              </button>
            </div>
          </div>
        ))}
      </div>
      {items.length < total && (
        <button className="btn" disabled={loading} onClick={more}>
          加载更多项目
        </button>
      )}
    </div>
  );
}

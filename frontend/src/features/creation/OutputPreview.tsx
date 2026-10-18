import { useEffect, useState, useRef, type MouseEvent } from "react";
import type { Project, RunView } from "../../lib/api";
import { presentProject } from "./projectPresentation";
export function OutputPreview({
  project,
  runView,
  readState,
}: {
  project: Project;
  runView: RunView | null;
  readState: "loading" | "ok" | "empty" | "error";
}) {
  const [error, setError] = useState(""),
    [checking, setChecking] = useState(false),
    epoch = useRef(0),
    locked = useRef(false);
  useEffect(() => {
    epoch.current++;
    setError("");
    setChecking(false);
    locked.current = false;
    return () => {
      epoch.current++;
    };
  }, [project.id, project.finalUrl]);
  if (!project.finalUrl && !runView?.outputs?.length) return null;
  const view = presentProject(project, runView, readState);
  const download = async (e: MouseEvent<HTMLAnchorElement>, url: string) => {
    const target = new URL(url, window.location.href);
    if (!["http:", "https:"].includes(target.protocol)) {
      e.preventDefault();
      setError("文件链接无效，请刷新版本记录。");
      return;
    }
    if (target.origin !== window.location.origin) return; // Native download/open for external signed resources; never append credentials.
    e.preventDefault();
    if (locked.current) return;
    const n = epoch.current;
    locked.current = true;
    setChecking(true);
    setError("");
    const c = new AbortController(),
      timeout = setTimeout(() => c.abort(), 15000);
    try {
      const r = await fetch(target.href, {
        method: "HEAD",
        credentials: "same-origin",
        signal: c.signal,
        cache: "no-store",
      });
      if (!r.ok)
        throw new Error(
          "文件无法下载，可能已过期或暂不可用。版本记录已保留，请刷新查询或联系支持。",
        );
      if (n !== epoch.current) return;
      const link = document.createElement("a");
      link.href = target.href;
      link.download = "";
      link.click();
    } catch (e) {
      if (n === epoch.current)
        setError(
          e instanceof Error && e.name !== "AbortError"
            ? e.message
            : "文件无法下载，请稍后重试。版本记录已保留。",
        );
    } finally {
      clearTimeout(timeout);
      if (n === epoch.current) {
        locked.current = false;
        setChecking(false);
      }
    }
  };
  return (
    <section className="card space-y-3" aria-label="成片与版本">
      <h2 className="text-lg font-bold">
        {view.output === "current" ? "本版成片" : "上一版本成片"}
      </h2>
      {project.finalUrl && (
        <>
          <video
            key={project.finalUrl}
            controls
            preload="none"
            className="w-full max-h-[420px] rounded bg-black"
            onError={() =>
              setError(
                "成片暂时无法加载，版本记录已保留。请刷新查询或在新窗口打开。",
              )
            }
          >
            <source src={project.finalUrl} type="video/mp4" />
          </video>
          <div className="ui-actions">
            <a
              className="btn btn-primary"
              href={project.finalUrl}
              download
              onClick={(e) => void download(e, project.finalUrl)}
            >
              下载成片
            </a>
            <a
              className="btn"
              href={project.finalUrl}
              target="_blank"
              rel="noopener noreferrer"
            >
              新窗口播放
            </a>
          </div>
        </>
      )}
      {checking && <p role="status">正在检查文件是否可用…</p>}
      {error && (
        <p role="alert" className="note note-err">
          {error}
        </p>
      )}
      {(runView?.outputs ?? [])
        .filter((o) => o.url !== project.finalUrl)
        .map((o) => (
          <a
            key={o.runId + o.url}
            className="btn"
            href={o.url}
            download
            onClick={(e) => void download(e, o.url)}
          >
            {o.projectVersion > 0
              ? `下载 v${o.projectVersion}`
              : "下载历史成片"}
          </a>
        ))}
    </section>
  );
}

import { useRef, useEffect, useState, useCallback } from "react";
import { Link, useNavigate } from "react-router-dom";
import { api, errorMessage, staleError, type Estimate } from "../lib/api";
import { useFocusScope } from "./useFocusScope";
import { Composer } from "../features/creation/Composer";
import {
  useCreationSession,
  defaultDraft,
} from "../features/creation/CreationProvider";
export function NewProjectDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: () => void;
}) {
  const { draft, setDraft, intent } = useCreationSession(),
    nav = useNavigate(),
    ref = useRef<HTMLDivElement>(null),
    [attempt, setAttempt] = useState(0),
    [value, setValue] = useState<{
      key: string;
      data: Estimate | null;
      error: string;
    }>({ key: "", data: null, error: "" });
  const estimateKey = JSON.stringify([
    draft.totalSeconds,
    draft.aspect,
    draft.filmReference,
    draft.mood,
  ]);
  const busy = ["creating-project", "starting-run"].includes(intent.phase),
    unknown = ["creation-unknown", "start-unknown"].includes(intent.phase);
  const close = useCallback(() => {
    if (!busy) onClose();
  }, [busy, onClose]);
  useFocusScope(ref, true, close);
  useEffect(() => {
    const c = new AbortController();
    let active = true;
    setValue({ key: estimateKey, data: null, error: "" });
    api
      .get<Estimate>(
        `/projects/estimate?totalSeconds=${draft.totalSeconds}`,
        c.signal,
      )
      .then((data) => {
        if (active) setValue({ key: estimateKey, data, error: "" });
      })
      .catch((e) => {
        if (active && !staleError(e))
          setValue({ key: estimateKey, data: null, error: errorMessage(e) });
      });
    return () => {
      active = false;
      c.abort();
    };
  }, [estimateKey, attempt]);
  const consumed = useRef(false);
  useEffect(() => {
    if (intent.phase === "ready" && intent.projectId && !consumed.current) {
      consumed.current = true;
      const id = intent.projectId;
      // Keep the 30s cross-tab cooldown after success; explicit user cleanup
      // still calls intent.clear() without this flag.
      intent.clear(true);
      setDraft({ ...defaultDraft });
      onCreated();
      nav(`/project/${id}`);
    }
  }, [intent.phase, intent.projectId]);
  return (
    <div className="dlg-backdrop" onClick={close}>
      <section
        className="dlg"
        ref={ref}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label="新建短片"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="dlg-head">
          <h2>新建短片</h2>
          <button
            className="dlg-x"
            onClick={close}
            disabled={busy}
            aria-label="关闭"
          >
            ×
          </button>
        </header>
        {unknown ? (
          <div className="dlg-body">
            <div role="alert" className="note note-info">
              {intent.error}
            </div>
            <button className="btn" onClick={() => void intent.check()}>
              核对原请求
            </button>
            {((intent.phase === "start-unknown" && intent.canRecoverRun) ||
              (intent.phase === "creation-unknown" &&
                intent.canRecoverCreation)) && (
              <button
                className="btn"
                onClick={() => void intent.retrySameIntent()}
              >
                恢复原提交
              </button>
            )}
            {((intent.phase === "creation-unknown" &&
              !intent.canRecoverCreation) ||
              (intent.phase === "start-unknown" && !intent.canRecoverRun)) && (
              <button className="btn" onClick={() => intent.clear()}>
                结束本机恢复
              </button>
            )}
            <Link
              className="btn"
              to={intent.projectId ? `/project/${intent.projectId}` : "/"}
              onClick={onClose}
            >
              查看项目
            </Link>
          </div>
        ) : (
          <Composer
            draft={draft}
            onChange={setDraft}
            onSubmit={() => void intent.submit(draft)}
            estimate={value.key === estimateKey ? value.data : null}
            busy={busy}
            error={intent.error || value.error}
            retryEstimate={() => setAttempt((n) => n + 1)}
          />
        )}
      </section>
    </div>
  );
}

import { useEffect, useState, useCallback, useRef } from "react";
import {
  api,
  ApiError,
  errorMessage,
  staleError,
  type Project,
  type RunView,
} from "../../lib/api";
import { projectsChanged } from "../../lib/projects";
import { toast } from "../../lib/format";
export function useProjectRun(id: string) {
  const [p, setP] = useState<Project | null>(null);
  const [runView, setRunView] = useState<RunView | null>(null);
  const [runState, setRunState] = useState<
    "loading" | "ok" | "empty" | "error"
  >("loading");
  const [runError, setRunError] = useState("");
  const [busy, setBusy] = useState("");

  const [error, setError] = useState(""),
    [actionError, setActionError] = useState<ApiError | null>(null);
  const latest = useRef(0),
    activeID = useRef(id),
    controller = useRef<AbortController>(),
    actionBusy = useRef<object | null>(null),
    actionEpoch = useRef(0);
  const inFlight = useRef(false),
    readFailed = useRef(false);
  activeID.current = id;
  const load = useCallback(
    async (force = false) => {
      if (activeID.current !== id || (inFlight.current && !force)) return;
      // A successful command needs a fresh read, not an in-flight pre-command
      // snapshot. Abort it and advance the epoch; passive polling still coalesces.
      inFlight.current = true;
      readFailed.current = false;
      const n = ++latest.current;
      controller.current?.abort();
      const c = new AbortController();
      controller.current = c;
      try {
        const d = await api.get<Project>(`/projects/${id}`, c.signal);
        if (n !== latest.current || activeID.current !== id) return;
        d.shots = d.shots ?? [];
        d.characters = d.characters ?? [];
        d.scenes = d.scenes ?? [];
        d.reviews = d.reviews ?? [];
        d.shots.forEach((s) => {
          s.frames = s.frames ?? [];
        });
        setP(d);
        setError("");
        try {
          const rv = await api.get<RunView | null>(
            `/projects/${id}/runs/latest`,
            c.signal,
          );
          if (n !== latest.current || activeID.current !== id) return;
          if (rv && rv.run) {
            setRunView(rv);
            setRunState("ok");
          } else {
            setRunView(null);
            setRunState("empty");
          }
          setRunError("");
        } catch (e: unknown) {
          if (
            n === latest.current &&
            activeID.current === id &&
            !staleError(e)
          ) {
            readFailed.current = true;
            // 查询失败不是“历史项目”的证据：保留错误状态，关闭依赖执行状态的写操作。
            setRunState("error");
            setRunError(errorMessage(e));
          }
        }
      } catch (e: unknown) {
        if (n === latest.current && activeID.current === id && !staleError(e)) {
          readFailed.current = true;
          setError(errorMessage(e));
        }
      } finally {
        if (n === latest.current) inFlight.current = false;
      }
    },
    [id],
  );
  useEffect(() => {
    inFlight.current = false;
    setP(null);
    setRunView(null);
    setRunState("loading");
    setRunError("");
    setError("");
    setActionError(null);
    setBusy("");
    actionEpoch.current++;
    actionBusy.current = null;
    load();
    return () => {
      latest.current++;
      actionEpoch.current++;
      actionBusy.current = null;
      controller.current?.abort();
    };
  }, [load]);
  const runActive =
    !!runView?.run &&
    ["queued", "running", "waiting_credits", "settlement_pending"].includes(
      runView.run.status,
    );
  const generating =
    !!p &&
    (p.status.endsWith("_GENERATING") ||
      p.status === "ASSEMBLING" ||
      p.generationJobs?.some((j) =>
        ["queued", "running", "reconcile"].includes(j.status),
      ) ||
      runActive);
  useEffect(() => {
    let stopped = false,
      polling = false,
      delay = runActive ? 5000 : 10000,
      timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      if (stopped || polling || document.hidden) return;
      polling = true;
      try {
        await load();
      } finally {
        polling = false;
        delay = readFailed.current
          ? Math.min(delay * 2, 30000)
          : runActive
            ? 5000
            : 10000;
        if (generating && !stopped && !document.hidden)
          timer = setTimeout(tick, delay);
      }
    };
    const visible = () => {
      clearTimeout(timer);
      if (!document.hidden) void tick();
    };
    if (generating) timer = setTimeout(tick, delay);
    document.addEventListener("visibilitychange", visible);
    window.addEventListener("online", visible);
    return () => {
      stopped = true;
      clearTimeout(timer);
      document.removeEventListener("visibilitychange", visible);
      window.removeEventListener("online", visible);
    };
  }, [generating, load, runActive]);
  const act = async (
    key: string,
    fn: () => Promise<unknown>,
    msg?: string | ((result: unknown) => string),
  ) => {
    if (actionBusy.current) return;
    const ticket = {},
      epoch = actionEpoch.current;
    actionBusy.current = ticket;
    setBusy(key);
    setActionError(null);
    const current = () =>
      actionBusy.current === ticket &&
      actionEpoch.current === epoch &&
      activeID.current === id;
    try {
      const result = await fn();
      if (!current()) return;
      if (msg) toast(typeof msg === "function" ? msg(result) : msg, "ok");
      projectsChanged();
      await load(true);
    } catch (e: unknown) {
      if (current() && !staleError(e))
        setActionError(
          e instanceof ApiError ? e : new ApiError(errorMessage(e)),
        );
    } finally {
      if (current()) {
        actionBusy.current = null;
        setBusy("");
      }
    }
  };

  return {
    project: p,
    runView,
    readState: runState,
    error,
    runError,
    busy,
    actionError,
    runActive,
    refresh: () => load(true),
    act,
  };
}

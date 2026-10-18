import { useEffect, useRef, useState } from "react";
import { useAuth } from "../../App";
import {
  api,
  ApiError,
  errorMessage,
  getSessionScope,
  useSessionScope,
  type Project,
  type StartRunResult,
} from "../../lib/api";
import { projectsChanged } from "../../lib/projects";
import {
  clearCreationGate,
  creationRequestKey,
  releaseCreationGate,
} from "./creationGate";
import type { CreationDraft, RunContext } from "./types";
const KEY = "drama_creation_intent";
type Phase =
  | "idle"
  | "creating-project"
  | "creation-unknown"
  | "starting-run"
  | "start-unknown"
  | "ready"
  | "error";
type Intent = {
  creationVersion?: 1;
  runIdentityVersion?: 1;
  account: string;
  session: string;
  requestKey: string;
  draft: CreationDraft;
  projectId?: string;
  runId?: string;
  phase: Phase;
  error: string;
};
function restore(account: string, session: string): Intent | null {
  try {
    const v = JSON.parse(
      sessionStorage.getItem(KEY) || "null",
    ) as Intent | null;
    if (v?.account !== account || v?.session !== session) return null;
    if (!v.requestKey || !v.draft) return null;
    return {
      ...v,
      phase:
        v.phase === "creating-project"
          ? "creation-unknown"
          : v.phase === "starting-run"
            ? "start-unknown"
            : v.phase,
    };
  } catch {
    return null;
  }
}
export function useCreateIntent() {
  const { user } = useAuth(),
    session = useSessionScope(),
    account = user?.id || "",
    identity = account + ":" + session;
  const [state, setState] = useState<Intent | null>(() =>
      restore(account, session),
    ),
    current = useRef(state),
    scope = useRef(identity),
    alive = useRef(true),
    locked = useRef(false);
  scope.current = identity;
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useEffect(() => {
    const next = restore(account, session);
    current.current = next;
    setState(next);
    locked.current = false;
  }, [identity]);
  const valid = (i: Intent) =>
    alive.current &&
    scope.current === i.account + ":" + i.session &&
    getSessionScope() === i.session &&
    current.current?.requestKey === i.requestKey;
  const save = (i: Intent) => {
    if (!valid(i)) return;
    current.current = i;
    setState(i);
    sessionStorage.setItem(KEY, JSON.stringify(i));
  };
  const start = async (i: Intent) => {
    save({ ...i, phase: "starting-run", error: "" });
    try {
      const r = await api.post<StartRunResult>(
        `/projects/${i.projectId}/runs`,
        {
          idempotencyKey: i.requestKey,
          mode: "first",
          scope: {
            totalSeconds: i.draft.totalSeconds,
            aspect: i.draft.aspect,
            filmReference: i.draft.filmReference,
            mood: i.draft.mood,
          },
        },
      );
      // The server has a run for this request identity; release the cross-tab
      // gate so a later, intentional same-draft project can start fresh.
      releaseCreationGate(i.account, i.draft, i.requestKey);
      if (!valid(i)) return;
      save({ ...i, runId: r.run?.id, phase: "ready", error: "" });
      projectsChanged();
    } catch (e) {
      if (valid(i))
        save({
          ...i,
          phase: "start-unknown",
          error: "启动结果待核对：" + errorMessage(e),
        });
    }
  };
  const create = async (i: Intent, replay = false) => {
    save({ ...i, phase: "creating-project", error: "" });
    try {
      const p = await api.post<Project>("/projects", {
        ...i.draft,
        idempotencyKey: i.requestKey,
      });
      if (!valid(i)) return;
      if (!p?.id) throw new Error("创建响应不完整");
      const created = { ...i, projectId: p.id };
      save(created);
      projectsChanged();
      await start(created);
    } catch (e) {
      if (valid(i)) {
        const rejected =
          !replay && e instanceof ApiError && e.code === "project_not_created";
        if (rejected)
          clearCreationGate(i.account, i.draft, i.requestKey);
        save({
          ...i,
          phase: rejected ? "error" : "creation-unknown",
          error: rejected
            ? errorMessage(e)
            : "创建结果待核对；恢复会复用原请求，不会重复创建或扣费。",
        });
      }
    }
  };
  const submit = async (draft: CreationDraft) => {
    if (
      locked.current ||
      (current.current &&
        current.current.phase !== "idle" &&
        current.current.phase !== "error")
    )
      return;
    if (draft.idea.trim().length < 5) return;
    if (!account || getSessionScope() !== session || scope.current !== identity)
      return;
    const normalized: CreationDraft = {
      ...draft,
      idea: draft.idea.trim(),
      filmReference: (draft.filmReference || "").trim(),
      mood: (draft.mood || "").trim(),
    };
    locked.current = true;
    // Two independent tabs with the same account/draft share this key through
    // localStorage + Web Locks, so the server sees one creation identity.
    const requestKey = await creationRequestKey(account, normalized);
    if (
      !alive.current ||
      getSessionScope() !== session ||
      scope.current !== identity
    ) {
      locked.current = false;
      return;
    }
    const i: Intent = {
      account,
      session,
      creationVersion: 1,
      runIdentityVersion: 1,
      requestKey,
      draft: normalized,
      phase: "creating-project",
      error: "",
    };
    current.current = i;
    save(i);
    try {
      await create(i);
    } finally {
      if (valid(i)) locked.current = false;
    }
  };
  const check = async () => {
    const i = current.current;
    if (!i || locked.current || !valid(i)) return;
    locked.current = true;
    try {
      if (!i.projectId) {
        if (i.creationVersion !== 1) {
          save({
            ...i,
            error:
              "旧版请求没有服务端创建标识，请先查看项目列表；结束本次创建只清理本机记录，不删除已有项目。",
          });
          projectsChanged();
          return;
        }
        const p = await api.get<Project | null>(
          `/projects/creation-context?requestKey=${encodeURIComponent(i.requestKey)}`,
        );
        if (!valid(i)) return;
        if (p?.id)
          save({
            ...i,
            projectId: p.id,
            phase: "start-unknown",
            error: "已找到原项目，可以恢复原提交。",
          });
        else
          save({
            ...i,
            error: "暂未查到原项目；恢复会复用原请求标识，不会另建重复项目。",
          });
        return;
      }
      const r = await api.get<RunContext | null>(
        `/runs/context?projectId=${encodeURIComponent(i.projectId)}&requestKey=${encodeURIComponent(i.requestKey)}`,
      );
      if (!valid(i)) return;
      if (r) {
        if (r.project.id !== i.projectId || r.run.projectId !== i.projectId)
          throw Error("执行归属不匹配");
        releaseCreationGate(i.account, i.draft, i.requestKey);
        save({ ...i, runId: r.run.id, phase: "ready", error: "" });
        projectsChanged();
      } else
        save({
          ...i,
          phase: "start-unknown",
          error:
            i.runIdentityVersion === 1 || i.creationVersion === 1
              ? "暂未查到原请求；恢复时会复用同一请求，不重复创建项目。"
              : "暂未查到旧版原请求，请先查看项目；系统不会自动重新提交。",
        });
    } catch (e) {
      if (valid(i)) save({ ...i, error: "查询失败：" + errorMessage(e) });
    } finally {
      if (valid(i)) locked.current = false;
    }
  };
  const retrySameIntent = async () => {
    const i = current.current;
    if (
      !i ||
      locked.current ||
      !valid(i) ||
      !["creation-unknown", "start-unknown"].includes(i.phase)
    )
      return;
    if (!i.projectId && i.creationVersion !== 1) {
      await check();
      return;
    }
    locked.current = true;
    try {
      if (!i.projectId) {
        const p = await api.get<Project | null>(
          `/projects/creation-context?requestKey=${encodeURIComponent(i.requestKey)}`,
        );
        if (!valid(i)) return;
        if (p?.id) {
          const found = { ...i, projectId: p.id };
          save(found);
          await start(found);
        } else await create(i, true);
      } else {
        const r = await api.get<RunContext | null>(
          `/runs/context?projectId=${encodeURIComponent(i.projectId)}&requestKey=${encodeURIComponent(i.requestKey)}`,
        );
        if (!valid(i)) return;
        if (r) {
          if (r.project.id !== i.projectId || r.run.projectId !== i.projectId)
            throw Error("执行归属不匹配");
          releaseCreationGate(i.account, i.draft, i.requestKey);
          save({ ...i, runId: r.run.id, phase: "ready", error: "" });
          projectsChanged();
        } else if (i.runIdentityVersion === 1 || i.creationVersion === 1) {
          // creationVersion=1 shipped with durable run aliases in 2b4faf7.
          await start(i);
        } else {
          save({
            ...i,
            error:
              "旧版提交结果无法确认，请先查看项目；结束本机恢复不会取消任务或重新生成。",
          });
        }
      }
    } catch (e) {
      if (valid(i)) save({ ...i, error: "恢复失败：" + errorMessage(e) });
    } finally {
      if (valid(i)) locked.current = false;
    }
  };
  const clear = (preserveCooldown = false) => {
    if (getSessionScope() !== session || scope.current !== identity) return;
    const intent = current.current;
    if (intent) {
      if (preserveCooldown)
        releaseCreationGate(intent.account, intent.draft, intent.requestKey);
      else
        clearCreationGate(intent.account, intent.draft, intent.requestKey);
    }
    current.current = null;
    setState(null);
    sessionStorage.removeItem(KEY);
    locked.current = false;
  };
  const s =
    state?.account === account && state.session === session ? state : null;
  return {
    canRecoverCreation: s?.creationVersion === 1,
    canRecoverRun: s?.runIdentityVersion === 1 || s?.creationVersion === 1,
    phase: s?.phase || ("idle" as Phase),
    error: s?.error || "",
    projectId: s?.projectId,
    runId: s?.runId,
    draft: s?.draft,
    submit,
    check,
    retrySameIntent,
    clear,
  };
}

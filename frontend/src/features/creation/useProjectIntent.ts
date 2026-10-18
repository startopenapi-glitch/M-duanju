import { useEffect, useRef } from "react";
import { api, ApiError, getSessionScope } from "../../lib/api";
import type { RunContext } from "./types";
type Intent = {
  runIdentityVersion?: 1;
  key: string;
  mode: string;
  scope: unknown;
  projectId: string;
  uncertain?: boolean;
};
function readPendingIntent(projectId: string): Intent | null {
  try {
    const saved = JSON.parse(
      sessionStorage.getItem(
        `drama_project_intent:${getSessionScope()}:${projectId}`,
      ) || "null",
    );
    if (
      saved?.projectId === projectId &&
      typeof saved.key === "string" &&
      saved.key.length > 0 &&
      saved.key.length <= 64 &&
      ["first", "revise"].includes(saved.mode) &&
      saved.scope &&
      typeof saved.scope === "object" &&
      !Array.isArray(saved.scope)
    )
      return saved;
  } catch {}
  return null;
}

// Restore only this account/project's unresolved instruction. This is a draft
// read, never a new generation command; submit still reconciles the original key.
export function pendingRevisionInstruction(projectId: string): string {
  const intent = readPendingIntent(projectId);
  const scope = intent?.scope as { instruction?: unknown } | undefined;
  return intent?.mode === "revise" && typeof scope?.instruction === "string"
    ? scope.instruction
    : "";
}

export function useProjectIntent(projectId: string) {
  const session = getSessionScope(),
    slot = `drama_project_intent:${session}:${projectId}`;
  const pending = useRef<{ slot: string; value: Intent | null }>({
    slot: "",
    value: null,
  });
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  if (pending.current.slot !== slot) {
    pending.current = { slot, value: readPendingIntent(projectId) };
  }
  const owns = (intent: Intent) =>
    alive.current &&
    getSessionScope() === session &&
    pending.current.slot === slot &&
    pending.current.value?.key === intent.key &&
    readPendingIntent(projectId)?.key === intent.key;
  const submit = async (mode: string, scope: unknown) => {
    if (getSessionScope() !== session)
      throw new Error("账号已变更，请重新打开项目");
    let intent = pending.current.value;
    if (intent) {
      // A replay cannot disprove an earlier in-flight attempt, even if this
      // particular retry is rejected before creation. Retain its original key.
      intent.uncertain = true;
      sessionStorage.setItem(slot, JSON.stringify(intent));
      const found = await api.get<RunContext | null>(
        `/runs/context?projectId=${encodeURIComponent(projectId)}&requestKey=${encodeURIComponent(intent.key)}`,
      );
      if (getSessionScope() !== session) throw new Error("账号已变更");
      if (!owns(intent)) return;
      if (found) {
        if (found.project.id !== projectId || found.run.projectId !== projectId)
          throw new Error("原执行归属不匹配");
        // The found run answers the OLD request identity, not a newly edited
        // instruction. Do not silently discard the new scope.
        if (
          intent.mode !== mode ||
          JSON.stringify(intent.scope) !== JSON.stringify(scope)
        )
          throw new Error(
            "原执行与当前修改要求不一致。请先查看原执行，或结束本机恢复后再提交新的修改。",
          );
        sessionStorage.removeItem(slot);
        pending.current.value = null;
        return "recovered";
      }
      if (intent.runIdentityVersion !== 1)
        throw new Error(
          "旧版提交结果无法确认，请先查看项目。结束本机恢复只清理本机记录，不会取消任务或重新生成。",
        );
      if (
        intent.mode !== mode ||
        JSON.stringify(intent.scope) !== JSON.stringify(scope)
      )
        throw new Error(
          "上次提交结果尚未确认，请保留原修改要求并重试查询，勿创建新版本。",
        );
    } else {
      intent = {
        key: crypto.randomUUID(),
        mode,
        scope,
        projectId,
        runIdentityVersion: 1,
      };
      // Persist before the command so a refresh cannot turn an unknown response into a new charge.
      sessionStorage.setItem(slot, JSON.stringify(intent));
      pending.current.value = intent;
    }
    try {
      await api.post(`/projects/${projectId}/runs`, {
        idempotencyKey: intent.key,
        mode: intent.mode,
        scope: intent.scope,
      });
    } catch (e) {
      if (
        owns(intent) &&
        e instanceof ApiError &&
        e.code === "run_not_created" &&
        !intent.uncertain
      ) {
        sessionStorage.removeItem(slot);
        pending.current.value = null;
      }
      throw e;
    }
    if (!owns(intent)) return;
    sessionStorage.removeItem(slot);
    pending.current.value = null;
    return "submitted";
  };
  const clearPending = () => {
    const intent = pending.current.value;
    if (!intent || !owns(intent)) return;
    sessionStorage.removeItem(slot);
    pending.current.value = null;
  };
  return Object.assign(submit, {
    hasPending: !!pending.current.value,
    hasLegacyPending:
      !!pending.current.value && pending.current.value.runIdentityVersion !== 1,
    clearPending,
    clearLegacyPending: () => {
      const intent = pending.current.value;
      if (!intent || !owns(intent) || intent.runIdentityVersion === 1) return;
      clearPending();
    },
  });
}

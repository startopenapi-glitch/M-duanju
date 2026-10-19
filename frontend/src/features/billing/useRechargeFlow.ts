import { useCallback, useEffect, useRef, useState } from "react";
import {
  api,
  errorMessage,
  getSessionScope,
  useSessionScope,
} from "../../lib/api";
import { useAuth } from "../../App";
import { projectsChanged } from "../../lib/projects";
import type { RunContext } from "../creation/types";
import type { CreditTransaction, OrderProgress, RechargeOrder } from "./types";
import { presentRecharge } from "./presentation";

type State = {
  balance: number | null;
  transactions: CreditTransaction[];
  orders: RechargeOrder[];
  payMode: "wechat" | "manual" | null;
  context: RunContext | null;
  progress: OrderProgress | null;
  orderId: string;
  qr?: string;
  h5Url?: string;
  phase: string;
  error: string;
  busy: boolean;
};
const initial = (orderId = ""): State => ({
  balance: null,
  transactions: [],
  orders: [],
  payMode: null,
  context: null,
  progress: null,
  orderId,
  phase: "loading",
  error: "",
  busy: false,
});
const validID = (id: string) => /^[a-zA-Z0-9_-]{1,64}$/.test(id);
const validRun = (id: string) => /^[a-fA-F0-9]{32}$/.test(id);

export function useRechargeFlow({
  runId = "",
  orderId = "",
}: {
  runId?: string;
  orderId?: string;
}) {
  const scope = useSessionScope(),
    { user } = useAuth();
  const originalScope = useRef(scope),
    generation = useRef(0),
    lock = useRef(false);
  const [state, set] = useState<State>(() => initial(orderId)),
    [attempt, retry] = useState(0);
  const ref = useRef(state);
  ref.current = state;
  const valid = useCallback(
    (n: number) =>
      generation.current === n && originalScope.current === getSessionScope(),
    [],
  );
  const readAccount = useCallback(async (signal?: AbortSignal) => {
    const [credits, orders] = await Promise.all([
      api.get<{ balance: number; transactions: CreditTransaction[] }>(
        "/credits",
        signal,
      ),
      api.get<{ items: RechargeOrder[] }>("/orders", signal),
    ]);
    return {
      balance: credits.balance,
      transactions: credits.transactions,
      orders: orders.items,
    };
  }, []);

  useEffect(() => {
    const n = ++generation.current,
      controller = new AbortController();
    if (scope !== originalScope.current) {
      set(initial());
      return;
    }
    set(initial(orderId));
    if ((runId && !validRun(runId)) || (orderId && !validID(orderId))) {
      set((s) => ({
        ...s,
        phase: "blocked",
        error: "订单或任务链接无效，请从原任务重新进入。",
      }));
      return;
    }
    async function boot() {
      try {
        const account = await readAccount(controller.signal);
        const mode = await api.get<{ enabled: boolean }>(
          "/pay/status",
          controller.signal,
        );
        // Order-bound context is verified by the single tracking owner below.
        const context =
          runId && !orderId
            ? await api.get<RunContext>(
                `/runs/context?runId=${encodeURIComponent(runId)}`,
                controller.signal,
              )
            : null;
        if (context && (context.run.id !== runId || !context.project.id))
          throw new Error("任务归属核对失败");
        if (valid(n))
          set((s) => ({
            ...s,
            ...account,
            payMode: mode.enabled ? "wechat" : "manual",
            context,
            phase: orderId ? "tracking" : "ready",
          }));
      } catch (e) {
        if (valid(n))
          set((s) => ({ ...s, error: errorMessage(e), phase: "error" }));
      }
    }
    void boot();
    return () => {
      generation.current++;
      controller.abort();
    };
  }, [scope, user?.id, runId, orderId, attempt, readAccount, valid]);

  const checkRef = useRef<() => void>(() => {});
  useEffect(() => {
    if (
      !state.orderId ||
      state.payMode === null ||
      scope !== originalScope.current
    )
      return;
    const id = state.orderId,
      n = generation.current,
      controller = new AbortController();
    let active = true,
      inFlight = false,
      timer: ReturnType<typeof setTimeout> | undefined;
    let delay = 2000,
      started = Date.now(),
      terminal = false,
      refreshed = false;
    const alive = () => active && valid(n);
    const clear = () => {
      if (timer) clearTimeout(timer);
      timer = undefined;
    };
    async function tick(manual = false) {
      clear();
      if (!alive() || inFlight || (!manual && document.hidden)) return;
      if (!manual && Date.now() - started >= 600000) {
        set((s) => ({ ...s, phase: "paused" }));
        return;
      }
      if (manual) {
        started = Date.now();
        terminal = false;
      }
      inFlight = true;
      try {
        let progress = await api.get<OrderProgress>(
          `/orders/${encodeURIComponent(id)}/progress`,
          controller.signal,
        );
        if (!alive()) return;
        if (progress.id !== id || (runId && progress.runId !== runId)) {
          terminal = true;
          throw new Error(
            "订单与原任务不一致，已停止查询和充值。请从原订单重新进入。",
          );
        }
        let context = progress.runId
          ? await api.get<RunContext>(
              `/runs/context?runId=${encodeURIComponent(progress.runId)}`,
              controller.signal,
            )
          : null;
        if (!alive()) return;
        if (
          context &&
          (context.run.id !== progress.runId ||
            context.project.id !== progress.projectId)
        ) {
          terminal = true;
          throw new Error("订单与任务归属不一致，已停止操作。");
        }
        // This endpoint reconciles money. Call it only after exact ownership/binding verification.
        if (
          progress.payMode === "wechat" &&
          progress.status === "pending" &&
          !progress.refund
        ) {
          await api.get(
            `/pay/status/${encodeURIComponent(id)}`,
            controller.signal,
          );
          if (!alive()) return;
          progress = await api.get<OrderProgress>(
            `/orders/${encodeURIComponent(id)}/progress`,
            controller.signal,
          );
          if (
            progress.id !== id ||
            (runId && progress.runId !== runId) ||
            (context && progress.runId !== context.run.id)
          ) {
            terminal = true;
            throw new Error("订单与原任务不一致");
          }
          if (progress.runId)
            context = await api.get<RunContext>(
              `/runs/context?runId=${encodeURIComponent(progress.runId)}`,
              controller.signal,
            );
        }
        if (!alive()) return;
        const view = presentRecharge(progress, context);
        const continued =
          progress.credited &&
          !view.refund &&
          ["queued", "running", "completed"].includes(view.continuation);
        // Publish facts immediately, but emit navigation only after account and
        // project cache refresh. Unmounting early would abort those reads.
        set((s) => ({
          ...s,
          progress,
          context,
          qr: progress.payment?.qr,
          h5Url: progress.payment?.h5Url,
          error: "",
          phase: progress.credited ? "credited" : "tracking",
        }));
        if (progress.credited && !refreshed) {
          const account = await readAccount(controller.signal);
          if (!alive()) return;
          refreshed = true;
          set((s) => ({ ...s, ...account }));
          projectsChanged();
        }
        if (continued && alive())
          set((s) => ({ ...s, phase: "continuation-observed" }));
        terminal =
          continued ||
          !!view.refund ||
          ["closed", "rejected", "refunded"].includes(progress.status) ||
          view.continuation === "superseded" ||
          (progress.credited && !progress.runId) ||
          ["cancelled", "failed", "settlement_pending"].includes(
            view.continuation,
          );
        delay =
          progress.payMode === "wechat" && !progress.credited ? 2000 : 5000;
      } catch (e) {
        if (alive()) {
          delay = Math.min(delay * 2, 30000);
          set((s) => ({
            ...s,
            error: errorMessage(e),
            phase: terminal ? "blocked" : "query-error",
          }));
        }
      } finally {
        inFlight = false;
        if (alive() && !terminal && !document.hidden)
          timer = setTimeout(() => void tick(), delay);
      }
    }
    const visible = () => {
      clear();
      if (!document.hidden && !terminal) void tick(true);
    };
    checkRef.current = () => void tick(true);
    document.addEventListener("visibilitychange", visible);
    window.addEventListener("online", visible);
    void tick();
    return () => {
      active = false;
      clear();
      controller.abort();
      checkRef.current = () => {};
      document.removeEventListener("visibilitychange", visible);
      window.removeEventListener("online", visible);
    };
  }, [state.orderId, state.payMode, scope, runId, valid, readAccount]);

  const recharge = async (amountCents: number) => {
    const s = ref.current,
      n = generation.current;
    if (
      lock.current ||
      !valid(n) ||
      s.phase !== "ready" ||
      s.orderId ||
      !s.payMode
    )
      return;
    if (
      !Number.isInteger(amountCents) ||
      ![10000, 50000, 300000].includes(amountCents)
    )
      return;
    if (
      runId &&
      (!s.context ||
        !s.context.isLatest ||
        s.context.hasUnresolvedSubmission ||
        s.context.run.status !== "waiting_credits" ||
        !s.context.run.autoResume)
    ) {
      set((s) => ({
        ...s,
        error: "原任务目前不接受关联充值，请返回项目查看最新状态。",
      }));
      return;
    }
    lock.current = true;
    set((s) => ({ ...s, busy: true, error: "" }));
    try {
      const r = await api.post<{
        id?: string;
        qr?: string;
        h5Url?: string;
        downlevel?: boolean;
      }>("/pay/create", {
        amountCents,
        channel: "native",
        ...(runId ? { runId } : {}),
      });
      if (!valid(n)) return;
      if (!r.id || !validID(r.id))
        throw new Error("下单结果未能确认，请查询充值记录，不要重复提交。");
      set((s) => ({
        ...s,
        orderId: r.id!,
        qr: r.qr,
        h5Url: r.h5Url,
        phase: "tracking",
        payMode: r.downlevel ? "manual" : s.payMode,
      }));
    } catch (e) {
      if (valid(n))
        set((s) => ({
          ...s,
          error: errorMessage(e),
          phase: "creation-unknown",
        }));
    } finally {
      lock.current = false;
      if (valid(n)) set((s) => ({ ...s, busy: false }));
    }
  };
  return {
    ...(scope === originalScope.current ? state : initial()),
    recharge,
    check: () => checkRef.current(),
    retry: () => retry((n) => n + 1),
    selectOrder: (id: string) => {
      if (validID(id) && !lock.current)
        set((s) => ({
          ...s,
          orderId: id,
          qr: undefined,
          h5Url: undefined,
          progress: null,
          context: null,
          error: "",
          phase: "tracking",
        }));
    },
  };
}

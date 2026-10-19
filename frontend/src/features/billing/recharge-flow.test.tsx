import { test, expect, vi, afterEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import { api, setToken } from "../../lib/api";
import { useRechargeFlow } from "./useRechargeFlow";
vi.mock("../../App", () => ({
  useAuth: () => ({ user: { id: "a" }, setUser: vi.fn() }),
}));
const rid = "11111111111111111111111111111111";
const ctx = {
  run: { id: rid, projectId: "p", status: "waiting_credits", autoResume: true },
  project: { id: "p", title: "原任务" },
  isLatest: true,
  hasUnresolvedSubmission: false,
  shortfallCredits: 100,
  availableCredits: 0,
};
const order = {
  id: "o",
  status: "paid",
  payMode: "wechat",
  amountCents: 10000,
  credits: 10000,
  credited: false,
  runId: rid,
  projectId: "p",
  paymentEvidence: "recorded",
  refund: null,
};
function mock(extra?: (p: string) => unknown) {
  return vi.spyOn(api, "get").mockImplementation(async (p) => {
    const r = extra?.(p);
    if (r !== undefined) return r;
    if (p === "/credits") return { balance: 0, transactions: [] };
    if (p === "/orders") return { items: [] };
    if (p === "/pay/status") return { enabled: true };
    if (p.startsWith("/orders/")) return order;
    if (p.startsWith("/runs/context")) return ctx;
    return { status: "paid" };
  });
}
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
test("paid_false_credit_is_not_success_and_no_generation_command", async () => {
  mock();
  const post = vi.spyOn(api, "post");
  const { result } = renderHook(() =>
    useRechargeFlow({ runId: rid, orderId: "o" }),
  );
  await waitFor(() => expect(result.current.progress?.id).toBe("o"));
  expect(result.current.phase).not.toBe("credited");
  expect(result.current.phase).not.toBe("continuation-observed");
  expect(post).not.toHaveBeenCalled();
});
test("order_binding_mismatch_blocks_context_and_payment_reconciliation", async () => {
  const get = mock((p) =>
    p === "/orders/o/progress"
      ? {
          ...order,
          runId: "22222222222222222222222222222222",
          status: "pending",
        }
      : undefined,
  );
  const { result } = renderHook(() =>
    useRechargeFlow({ runId: rid, orderId: "o" }),
  );
  await waitFor(() => expect(result.current.error).toContain("不一致"));
  expect(get.mock.calls.some(([p]) => p === "/pay/status/o")).toBe(false);
  const post = vi.spyOn(api, "post");
  await act(() => result.current.recharge(10000));
  expect(post).not.toHaveBeenCalled();
});
test("manual_downlevel_retains_order_for_tracking", async () => {
  mock((p) =>
    p === "/orders/o/progress"
      ? {
          ...order,
          payMode: "manual",
          status: "pending",
          paymentEvidence: "manual",
        }
      : undefined,
  );
  vi.spyOn(api, "post").mockResolvedValue({ id: "o", downlevel: true });
  const { result } = renderHook(() => useRechargeFlow({ runId: rid }));
  await waitFor(() => expect(result.current.phase).toBe("ready"));
  await act(() => result.current.recharge(10000));
  await waitFor(() => expect(result.current.progress?.id).toBe("o"));
  expect(result.current.orderId).toBe("o");
});
test("late_paid_event_cannot_redirect_new_account", async () => {
  let finish!: (v: any) => void;
  mock((p) =>
    p === "/orders/o/progress" ? new Promise((r) => (finish = r)) : undefined,
  );
  setToken("A");
  const { result, unmount } = renderHook(() =>
    useRechargeFlow({ orderId: "o" }),
  );
  await waitFor(() => expect(finish).toBeTypeOf("function"));
  act(() => setToken("B"));
  await act(async () => finish({ ...order, credited: true }));
  expect(result.current.progress?.credited).not.toBe(true);
  unmount();
});
test("hidden_query_cannot_spawn_second_timer", async () => {
  let finish!: (v: any) => void;
  let calls = 0;
  mock((p) => {
    if (p === "/orders/o/progress") {
      calls++;
      return new Promise((r) => (finish = r));
    }
  });
  const { unmount } = renderHook(() => useRechargeFlow({ orderId: "o" }));
  await waitFor(() => expect(calls).toBe(1));
  for (let i = 0; i < 5; i++)
    document.dispatchEvent(new Event("visibilitychange"));
  expect(calls).toBe(1);
  vi.spyOn(document, "hidden", "get").mockReturnValue(true);
  await act(async () => finish(order));
  unmount();
});
test("continuation_is_not_emitted_before_account_refresh", async () => {
  let calls = 0,
    finish!: (v: any) => void;
  mock((p) => {
    if (p === "/credits" && ++calls > 1)
      return new Promise((r) => (finish = r));
    if (p === "/orders/o/progress") return { ...order, credited: true };
    if (p.startsWith("/runs/context"))
      return { ...ctx, run: { ...ctx.run, status: "running" } };
  });
  const h = renderHook(() => useRechargeFlow({ orderId: "o", runId: rid }));
  await waitFor(() => expect(finish).toBeTypeOf("function"));
  expect(h.result.current.phase).not.toBe("continuation-observed");
  await act(async () => finish({ balance: 10000, transactions: [] }));
  await waitFor(() =>
    expect(h.result.current.phase).toBe("continuation-observed"),
  );
  expect(h.result.current.balance).toBe(10000);
});

import { test, expect, vi, afterEach } from "vitest";
import {
  renderHook,
  render,
  screen,
  act,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { api } from "../../lib/api";
import { useRechargeFlow } from "./useRechargeFlow";
import Credits from "../../pages/Credits";
vi.mock("../../App", () => ({
  useAuth: () => ({ user: { id: "review-user" }, setUser: vi.fn() }),
}));
const rid = "11111111111111111111111111111111";
const ctx = {
  run: { id: rid, projectId: "p", status: "waiting_credits", autoResume: true },
  project: { id: "p", title: "原项目-雨夜" },
  isLatest: true,
  hasUnresolvedSubmission: false,
  shortfallCredits: 100,
  availableCredits: 0,
};
const order = {
  id: "review-order",
  status: "pending",
  payMode: "manual",
  amountCents: 10000,
  credits: 10000,
  credited: false,
  runId: rid,
  projectId: "p",
  paymentEvidence: "manual",
  refund: null,
};
function mock(extra: (p: string) => unknown = () => undefined) {
  return vi.spyOn(api, "get").mockImplementation(async (path) => {
    const r = extra(path);
    if (r !== undefined) return r;
    if (path === "/credits") return { balance: 0, transactions: [] };
    if (path === "/orders") return { items: [] };
    if (path === "/pay/status") return { enabled: true };
    if (path.startsWith("/orders/")) return order;
    if (path.startsWith("/runs/context")) return ctx;
    return { status: "pending" };
  });
}
afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  sessionStorage.clear();
});

test("review: credited order survives failed balance refresh and retries without any command", async () => {
  let reads = 0;
  mock((p) => {
    if (p === "/credits") {
      reads++;
      if (reads === 2)
        return Promise.reject(new Error("balance temporarily unavailable"));
      return { balance: reads === 1 ? 0 : 10000, transactions: [] };
    }
    if (p.startsWith("/orders/"))
      return { ...order, status: "paid", credited: true };
    if (p.startsWith("/runs/context"))
      return { ...ctx, run: { ...ctx.run, status: "running" } };
  });
  const post = vi.spyOn(api, "post");
  const h = renderHook(() =>
    useRechargeFlow({ runId: rid, orderId: order.id }),
  );
  await waitFor(() => expect(h.result.current.phase).toBe("query-error"));
  expect(h.result.current.progress?.credited).toBe(true);
  expect(h.result.current.balance).toBe(0);
  await act(async () => h.result.current.check());
  await waitFor(() =>
    expect(h.result.current.phase).toBe("continuation-observed"),
  );
  expect(h.result.current.balance).toBe(10000);
  expect(post).not.toHaveBeenCalled();
  h.unmount();
});

test("review: credited cancelled original run does not claim continuation or post", async () => {
  mock((p) =>
    p.startsWith("/orders/")
      ? { ...order, status: "paid", credited: true }
      : p.startsWith("/runs/context")
        ? { ...ctx, run: { ...ctx.run, status: "cancelled" } }
        : undefined,
  );
  const post = vi.spyOn(api, "post");
  const h = renderHook(() =>
    useRechargeFlow({ runId: rid, orderId: order.id }),
  );
  await waitFor(() =>
    expect(h.result.current.context?.run.status).toBe("cancelled"),
  );
  expect(h.result.current.phase).not.toBe("continuation-observed");
  expect(post).not.toHaveBeenCalled();
  h.unmount();
});

test("review: returning after ten minutes hidden rechecks order before pausing", async () => {
  let paid = false;
  const get = mock((p) =>
    p.startsWith("/orders/")
      ? { ...order, status: paid ? "paid" : "pending", credited: paid }
      : p.startsWith("/runs/context")
        ? {
            ...ctx,
            run: { ...ctx.run, status: paid ? "running" : "waiting_credits" },
          }
        : undefined,
  );
  const h = renderHook(() =>
    useRechargeFlow({ runId: rid, orderId: order.id }),
  );
  await waitFor(() => expect(h.result.current.progress?.id).toBe(order.id));
  const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(true);
  act(() => document.dispatchEvent(new Event("visibilitychange")));
  vi.useFakeTimers();
  await act(() => vi.advanceTimersByTimeAsync(600001));
  paid = true;
  hidden.mockReturnValue(false);
  await act(async () => document.dispatchEvent(new Event("visibilitychange")));
  expect(
    get.mock.calls.filter(([p]) => p === "/orders/review-order/progress"),
  ).toHaveLength(2);
  expect(h.result.current.phase).toBe("continuation-observed");
  h.unmount();
});

test("review: reopen pending native order retains a payment path for the same order", async () => {
  const qr = "data:image/png;base64,review-qr";
  mock((p) =>
    p.startsWith("/orders/")
      ? {
          ...order,
          payMode: "wechat",
          paymentEvidence: "unknown",
          payment: {
            qr,
            expiresAt: new Date(Date.now() + 1800000).toISOString(),
          },
        }
      : undefined,
  );
  const post = vi.spyOn(api, "post").mockResolvedValue({ id: order.id, qr });
  const first = renderHook(() => useRechargeFlow({ runId: rid }));
  await waitFor(() => expect(first.result.current.phase).toBe("ready"));
  await act(() => first.result.current.recharge(10000));
  await waitFor(() => expect(first.result.current.progress?.id).toBe(order.id));
  expect(first.result.current.qr).toBe(qr);
  first.unmount();
  const reopened = renderHook(() =>
    useRechargeFlow({ runId: rid, orderId: order.id }),
  );
  await waitFor(() =>
    expect(reopened.result.current.progress?.id).toBe(order.id),
  );
  expect(post).toHaveBeenCalledTimes(1);
  expect(
    reopened.result.current.qr || reopened.result.current.h5Url,
  ).toBeTruthy();
  reopened.unmount();
});

test("review: run-bound recharge identifies its project before taking payment", async () => {
  mock();
  render(
    <MemoryRouter initialEntries={["/credits?runId=" + rid]}>
      <Credits />
    </MemoryRouter>,
  );
  await waitFor(() =>
    expect(
      screen.getAllByRole("button", { name: "微信扫码支付" }),
    ).toHaveLength(3),
  );
  expect(screen.getByText("原项目-雨夜")).toBeTruthy();
});

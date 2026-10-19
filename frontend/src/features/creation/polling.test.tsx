import { test, expect, vi, afterEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import { api } from "../../lib/api";
import { useProjectRun } from "./useProjectRun";
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});
test("polling_stays_single_flight_after_visibility_burst", async () => {
  const p = {
    id: "p",
    status: "SHOTS_GENERATING",
    shots: [],
    characters: [],
    reviews: [],
    scenes: [],
    generationJobs: [],
  };
  let count = 0,
    finish!: (v: any) => void;
  vi.spyOn(api, "get").mockImplementation(async (path) => {
    if (path.endsWith("latest")) return { run: { id: "r", status: "running" } };
    count++;
    if (count === 2) return new Promise((r) => (finish = r));
    return p;
  });
  const h = renderHook(() => useProjectRun("p"));
  await waitFor(() => expect(h.result.current.runActive).toBe(true));
  vi.useFakeTimers();
  for (let i = 0; i < 6; i++)
    act(() => document.dispatchEvent(new Event("visibilitychange")));
  expect(count).toBe(2);
  await act(async () => finish(p));
  expect(vi.getTimerCount()).toBe(1);
  const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(true);
  act(() => document.dispatchEvent(new Event("visibilitychange")));
  await act(() => vi.advanceTimersByTimeAsync(30000));
  expect(count).toBe(2);
  hidden.mockReturnValue(false);
  await act(async () => document.dispatchEvent(new Event("visibilitychange")));
  expect(count).toBe(3);
  h.unmount();
  expect(vi.getTimerCount()).toBe(0);
});

test("polling_errors_back_off_and_online_rechecks_without_parallel_work", async () => {
  let reads = 0;
  vi.spyOn(api, "get").mockImplementation(async (path) => {
    if (path.endsWith("latest")) return { run: { id: "r", status: "running" } };
    reads++;
    if (reads > 1) throw Error("offline");
    return {
      id: "p",
      status: "SHOTS_GENERATING",
      shots: [],
      characters: [],
      reviews: [],
      scenes: [],
    };
  });
  const h = renderHook(() => useProjectRun("p"));
  await waitFor(() => expect(h.result.current.runActive).toBe(true));
  vi.useFakeTimers();
  await act(async () => document.dispatchEvent(new Event("visibilitychange")));
  expect(reads).toBe(2);
  await act(() => vi.advanceTimersByTimeAsync(5000));
  expect(reads).toBe(2);
  await act(() => vi.advanceTimersByTimeAsync(5000));
  expect(reads).toBe(3);
  await act(async () => window.dispatchEvent(new Event("online")));
  expect(reads).toBe(4);
  expect(vi.getTimerCount()).toBe(1);
  h.unmount();
});

test("successful_command_refreshes_after_inflight_old_read", async () => {
  let latest = 0,
    finish!: (v: any) => void;
  vi.spyOn(api, "get").mockImplementation(async (path) => {
    if (!path.endsWith("latest"))
      return {
        id: "p",
        status: "COMPLETED",
        shots: [],
        characters: [],
        scenes: [],
        reviews: [],
      };
    latest++;
    if (latest === 2) return new Promise((r) => (finish = r));
    return {
      run: {
        id: latest === 1 ? "old" : "new",
        status: latest === 1 ? "completed" : "running",
      },
    };
  });
  const h = renderHook(() => useProjectRun("p"));
  await waitFor(() => expect(h.result.current.readState).toBe("ok"));
  act(() => {
    void h.result.current.refresh();
  });
  await waitFor(() => expect(finish).toBeTypeOf("function"));
  await act(() => h.result.current.act("revise", async () => ({})));
  await act(async () => finish({ run: { id: "old", status: "completed" } }));
  expect(h.result.current.runView?.run.id).toBe("new");
  expect(h.result.current.runActive).toBe(true);
  expect(latest).toBe(3);
  h.unmount();
});

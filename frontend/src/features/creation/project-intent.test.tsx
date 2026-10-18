import { test, expect, vi, afterEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { api } from "../../lib/api";
import { useProjectIntent } from "./useProjectIntent";
afterEach(() => {
  vi.restoreAllMocks();
  sessionStorage.clear();
});
test("unknown_revision_keeps_same_key_after_reload", async () => {
  const post = vi
    .spyOn(api, "post")
    .mockRejectedValueOnce(new Error("lost response"))
    .mockResolvedValue({});
  vi.spyOn(api, "get").mockResolvedValue(null);
  const a = renderHook(() => useProjectIntent("p"));
  await act(async () => {
    await expect(
      a.result.current("revise", { instruction: "test" }),
    ).rejects.toThrow();
  });
  const key = (post.mock.calls[0][1] as any).idempotencyKey;
  a.unmount();
  const b = renderHook(() => useProjectIntent("p"));
  await act(() => b.result.current("revise", { instruction: "test" }));
  expect((post.mock.calls[1][1] as any).idempotencyKey).toBe(key);
});
test("known_original_result_is_read_without_reposting", async () => {
  const post = vi
    .spyOn(api, "post")
    .mockRejectedValue(new Error("lost response"));
  const a = renderHook(() => useProjectIntent("p"));
  await act(async () => {
    try {
      await a.result.current("first", {});
    } catch {}
  });
  vi.spyOn(api, "get").mockResolvedValue({
    project: { id: "p" },
    run: { id: "r", projectId: "p" },
  });
  await act(() => a.result.current("first", {}));
  expect(post).toHaveBeenCalledTimes(1);
});
test("definitive_rejection_allows_correcting_instruction", async () => {
  vi.spyOn(api, "get").mockResolvedValue(null);
  const { ApiError } = await import("../../lib/api");
  const post = vi
    .spyOn(api, "post")
    .mockRejectedValueOnce(
      new ApiError("无法形成修改范围", {
        status: 409,
        code: "run_not_created",
      }),
    )
    .mockResolvedValue({});
  const h = renderHook(() => useProjectIntent("p"));
  await act(async () => {
    await expect(
      h.result.current("revise", { instruction: "old" }),
    ).rejects.toThrow();
  });
  await act(() => h.result.current("revise", { instruction: "corrected" }));
  expect((post.mock.calls[1][1] as any).scope.instruction).toBe("corrected");
  expect((post.mock.calls[1][1] as any).idempotencyKey).not.toBe(
    (post.mock.calls[0][1] as any).idempotencyKey,
  );
});

test("unclassified_409_cannot_discard_original_intent", async () => {
  const { ApiError } = await import("../../lib/api");
  const post = vi
    .spyOn(api, "post")
    .mockRejectedValue(new ApiError("temporary view failure", { status: 409 }));
  vi.spyOn(api, "get").mockResolvedValue(null);
  const h = renderHook(() => useProjectIntent("p"));
  await act(async () => {
    await expect(
      h.result.current("revise", { instruction: "old" }),
    ).rejects.toThrow();
  });
  await act(async () => {
    await expect(
      h.result.current("revise", { instruction: "changed" }),
    ).rejects.toThrow();
  });
  expect(post).toHaveBeenCalledTimes(1);
});

test("replay_rejection_cannot_clear_an_earlier_unknown_attempt", async () => {
  const { ApiError } = await import("../../lib/api");
  const post = vi
    .spyOn(api, "post")
    .mockRejectedValueOnce(new TypeError("lost response"))
    .mockRejectedValueOnce(
      new ApiError("replay rejected", { status: 409, code: "run_not_created" }),
    );
  vi.spyOn(api, "get").mockResolvedValue(null);
  const h = renderHook(() => useProjectIntent("p"));
  for (let i = 0; i < 2; i++)
    await act(async () => {
      await expect(
        h.result.current("revise", { instruction: "old" }),
      ).rejects.toThrow();
    });
  await act(async () => {
    await expect(
      h.result.current("revise", { instruction: "changed" }),
    ).rejects.toThrow(/上次提交/);
  });
  expect(post).toHaveBeenCalledTimes(2);
  expect((post.mock.calls[0][1] as any).idempotencyKey).toBe(
    (post.mock.calls[1][1] as any).idempotencyKey,
  );
});

test("restored_inflight_intent_is_unknown_even_without_a_caught_error", async () => {
  const { ApiError } = await import("../../lib/api");
  const post = vi
    .spyOn(api, "post")
    .mockImplementationOnce(() => new Promise(() => {}))
    .mockRejectedValueOnce(
      new ApiError("retry rejected", { status: 409, code: "run_not_created" }),
    );
  vi.spyOn(api, "get").mockResolvedValue(null);
  const first = renderHook(() => useProjectIntent("p"));
  act(() => {
    void first.result.current("revise", { instruction: "old" });
  });
  first.unmount();
  const restored = renderHook(() => useProjectIntent("p"));
  await act(async () => {
    await expect(
      restored.result.current("revise", { instruction: "old" }),
    ).rejects.toThrow();
  });
  await act(async () => {
    await expect(
      restored.result.current("revise", { instruction: "changed" }),
    ).rejects.toThrow(/上次提交/);
  });
  expect(post).toHaveBeenCalledTimes(2);
  restored.unmount();
});

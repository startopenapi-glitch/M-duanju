import { afterEach, expect, test, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { api, getSessionScope } from "../../lib/api";
import { useCreateIntent } from "./useCreateIntent";
vi.mock("../../App", () => ({
  useAuth: () => ({ user: { id: "a" }, setUser: vi.fn() }),
}));
const draft = {
  idea: "一段可以恢复的故事",
  aspect: "16:9" as const,
  totalSeconds: 15,
  filmReference: "",
  mood: "",
};
afterEach(() => {
  vi.restoreAllMocks();
  sessionStorage.clear();
});
test("lost creation response recovers the original project and starts one run", async () => {
  const post = vi
    .spyOn(api, "post")
    .mockRejectedValueOnce(new TypeError("response lost"))
    .mockResolvedValue({ run: { id: "r" } });
  const get = vi.spyOn(api, "get").mockResolvedValue({ id: "p" });
  const first = renderHook(() => useCreateIntent());
  await act(() => first.result.current.submit(draft));
  expect(first.result.current.phase).toBe("creation-unknown");
  const body = post.mock.calls[0][1] as any;
  expect(body.idempotencyKey).toEqual(expect.any(String));
  first.unmount();
  const h = renderHook(() => useCreateIntent());
  await act(() => h.result.current.retrySameIntent());
  expect(get.mock.calls[0][0]).toContain(
    encodeURIComponent(body.idempotencyKey),
  );
  expect(post.mock.calls.filter(([p]) => p === "/projects")).toHaveLength(1);
  expect(post.mock.calls[1]).toEqual([
    "/projects/p/runs",
    expect.objectContaining({ idempotencyKey: body.idempotencyKey }),
  ]);
  expect(h.result.current.phase).toBe("ready");
});
test("undelivered creation replays same key and payload, not a second intent", async () => {
  const post = vi
    .spyOn(api, "post")
    .mockRejectedValueOnce(new TypeError("offline"))
    .mockResolvedValueOnce({ id: "p" })
    .mockResolvedValue({ run: { id: "r" } });
  vi.spyOn(api, "get").mockResolvedValue(null);
  const h = renderHook(() => useCreateIntent());
  await act(() => h.result.current.submit(draft));
  await act(() => h.result.current.retrySameIntent());
  expect(post.mock.calls[1]).toEqual(post.mock.calls[0]);
  expect(post.mock.calls).toHaveLength(3);
  expect(h.result.current.phase).toBe("ready");
});
test("legacy unknown creation is never replayed without a server identity", async () => {
  sessionStorage.setItem(
    "drama_creation_intent",
    JSON.stringify({
      account: "a",
      session: getSessionScope(),
      requestKey: "old-unused-key",
      draft,
      phase: "creation-unknown",
      error: "unknown",
    }),
  );
  const post = vi.spyOn(api, "post");
  const h = renderHook(() => useCreateIntent());
  await act(() => h.result.current.retrySameIntent());
  expect(post).not.toHaveBeenCalled();
  expect(h.result.current.phase).toBe("creation-unknown");
});

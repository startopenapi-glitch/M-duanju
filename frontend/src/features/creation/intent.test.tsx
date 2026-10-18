import { test, expect, vi, afterEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { api, setToken } from "../../lib/api";
import { useCreateIntent } from "./useCreateIntent";
vi.mock("../../App", () => ({
  useAuth: () => ({ user: { id: "a" }, setUser: vi.fn() }),
}));
const draft = {
  idea: "一个足够长的创作想法",
  aspect: "16:9" as const,
  totalSeconds: 15,
  filmReference: "教父式低调布光",
  mood: "克制、有张力",
};
afterEach(() => {
  vi.restoreAllMocks();
  sessionStorage.clear();
});
test("unknown_project_create_is_not_reposted_by_submit_or_query", async () => {
  const post = vi
    .spyOn(api, "post")
    .mockRejectedValue(new TypeError("network lost"));
  const { result } = renderHook(() => useCreateIntent());
  await act(() => result.current.submit(draft));
  expect(result.current.phase).toBe("creation-unknown");
  vi.spyOn(api, "get").mockResolvedValue(null);
  await act(() => result.current.check());
  await act(() => result.current.submit(draft));
  expect(post).toHaveBeenCalledTimes(1);
});
test("unknown_run_start_keeps_request_key", async () => {
  const post = vi.spyOn(api, "post").mockImplementation(async (path) => {
    if (path === "/projects") return { id: "p" };
    throw new TypeError("lost");
  });
  vi.spyOn(api, "get").mockResolvedValue(null);
  const { result } = renderHook(() => useCreateIntent());
  await act(() => result.current.submit(draft));
  expect(result.current.phase).toBe("start-unknown");
  await act(() => result.current.check());
  await act(() => result.current.retrySameIntent());
  const calls = post.mock.calls.filter(([p]) => p === "/projects/p/runs");
  expect(calls).toHaveLength(2);
  expect(calls[0][1]).toEqual(calls[1][1]);
  expect(post.mock.calls.filter(([p]) => p === "/projects")).toHaveLength(1);
});
test("double_submit_has_one_intent_and_late_account_response_is_ignored", async () => {
  let done!: (v: any) => void;
  const post = vi
    .spyOn(api, "post")
    .mockImplementation(() => new Promise((r) => (done = r)));
  setToken("A");
  const { result } = renderHook(() => useCreateIntent());
  let pending!: Promise<void>;
  act(() => {
    pending = result.current.submit(draft);
    void result.current.submit(draft);
  });
  // Cross-tab request-key coordination is async by design; let it settle.
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
  expect(post).toHaveBeenCalledTimes(1);
  act(() => setToken("B"));
  await act(async () => {
    done({ id: "old" });
    await pending;
  });
  expect(post).toHaveBeenCalledTimes(1);
  expect(result.current.projectId).not.toBe("old");
});

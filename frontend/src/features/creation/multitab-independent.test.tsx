import { test, expect, vi, afterEach, beforeEach } from "vitest";
import { renderHook, act, cleanup } from "@testing-library/react";
import { api, getSessionScope } from "../../lib/api";
import { useProjectIntent } from "./useProjectIntent";
import { useCreateIntent } from "./useCreateIntent";
vi.mock("../../App", () => ({ useAuth: () => ({ user: { id: "u" } }) }));
const scope = { totalSeconds: 15, aspect: "16:9" as const };
const draft = {
  idea: "这是一个测试故事",
  ...scope,
  filmReference: "film",
  mood: "mood",
};
beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  localStorage.setItem("drama_session_revision", "same-account-session");
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("SAFETY: historical unbound project alias must only reconcile and must not authorize a replacement run", async () => {
  sessionStorage.setItem(
    `drama_project_intent:${getSessionScope()}:p`,
    JSON.stringify({
      key: "historical-alias",
      mode: "first",
      scope,
      projectId: "p",
    }),
  );
  vi.spyOn(api, "get").mockResolvedValue(null);
  const post = vi
    .spyOn(api, "post")
    .mockResolvedValue({ run: { id: "replacement-run" } });
  const h = renderHook(() => useProjectIntent("p"));
  await act(async () => {
    try {
      await h.result.current("first", scope);
    } catch {}
  });
  expect(post).not.toHaveBeenCalled();
});
test("SAFETY: historical creation with projectId and no durable run alias must not authorize a replacement run", async () => {
  sessionStorage.setItem(
    "drama_creation_intent",
    JSON.stringify({
      account: "u",
      session: getSessionScope(),
      requestKey: "historical-alias",
      draft,
      projectId: "p",
      phase: "start-unknown",
      error: "",
    }),
  );
  vi.spyOn(api, "get").mockResolvedValue(null);
  const post = vi
    .spyOn(api, "post")
    .mockResolvedValue({ run: { id: "replacement-run" } });
  const h = renderHook(() => useCreateIntent());
  await act(async () => {
    await h.result.current.retrySameIntent();
  });
  expect(post).not.toHaveBeenCalled();
});
test("SAFE CONTROL: original cancelled run found through context clears pending without POST", async () => {
  sessionStorage.setItem(
    `drama_project_intent:${getSessionScope()}:p`,
    JSON.stringify({
      key: "original-key",
      mode: "first",
      scope,
      projectId: "p",
    }),
  );
  vi.spyOn(api, "get").mockResolvedValue({
    project: { id: "p" },
    run: { id: "r", projectId: "p", status: "cancelled" },
  });
  const post = vi.spyOn(api, "post").mockResolvedValue({});
  const h = renderHook(() => useProjectIntent("p"));
  let result;
  await act(async () => {
    result = await h.result.current("first", scope);
  });
  expect(result).toBe("recovered");
  expect(post).not.toHaveBeenCalled();
});
test("SAFE CONTROL: historical creation without projectId checks only and retains local record", async () => {
  sessionStorage.setItem(
    "drama_creation_intent",
    JSON.stringify({
      account: "u",
      session: getSessionScope(),
      requestKey: "legacy-key",
      draft,
      phase: "creation-unknown",
      error: "",
    }),
  );
  const post = vi.spyOn(api, "post").mockResolvedValue({});
  const h = renderHook(() => useCreateIntent());
  await act(async () => {
    await h.result.current.retrySameIntent();
  });
  expect(post).not.toHaveBeenCalled();
  expect(h.result.current.error).toContain("旧版");
});
test("SAFE CONTROL: account revision change prevents in-flight create response from starting a run", async () => {
  let finish!: (x: unknown) => void;
  const post = vi.spyOn(api, "post").mockImplementation(
    () =>
      new Promise((r) => {
        finish = r;
      }),
  );
  const h = renderHook(() => useCreateIntent());
  let operation!: Promise<void>;
  act(() => {
    operation = h.result.current.submit(draft);
  });
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
  localStorage.setItem("drama_session_revision", "other-account-session");
  await act(async () => {
    finish({ id: "created-under-old-account" });
    await operation;
  });
  expect(post).toHaveBeenCalledTimes(1);
});

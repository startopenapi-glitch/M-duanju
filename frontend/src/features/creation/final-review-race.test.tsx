import { test, expect, vi, afterEach, beforeEach } from "vitest";
import { renderHook, act, cleanup } from "@testing-library/react";
import { api, getSessionScope } from "../../lib/api";
import { useCreateIntent } from "./useCreateIntent";
vi.mock("../../App", () => ({ useAuth: () => ({ user: { id: "u" } }) }));
const draft = {
  idea: "这是另一个新测试故事",
  totalSeconds: 15,
  aspect: "16:9" as const,
  filmReference: "film",
  mood: "mood",
};
beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  localStorage.setItem("drama_session_revision", "same-session");
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
test("clearing legacy run recovery invalidates its delayed context before new intent is saved", async () => {
  sessionStorage.setItem(
    "drama_creation_intent",
    JSON.stringify({
      account: "u",
      session: getSessionScope(),
      requestKey: "legacy-alias",
      draft,
      projectId: "old-p",
      phase: "start-unknown",
      error: "",
    }),
  );
  let resolveOld!: (v: unknown) => void;
  vi.spyOn(api, "get").mockImplementation(
    () =>
      new Promise((r) => {
        resolveOld = r;
      }),
  );
  let resolveCreate!: (v: unknown) => void;
  vi.spyOn(api, "post").mockImplementation((path) =>
    path === "/projects"
      ? new Promise((r) => {
          resolveCreate = r;
        })
      : Promise.reject(new Error("new start result unknown")),
  );
  const h = renderHook(() => useCreateIntent());
  let oldCheck!: Promise<void>;
  act(() => {
    oldCheck = h.result.current.check();
  });
  act(() => h.result.current.clear());
  let newSubmit!: Promise<void>;
  act(() => {
    newSubmit = h.result.current.submit(draft);
  });
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
  const newKey = JSON.parse(
    sessionStorage.getItem("drama_creation_intent")!,
  ).requestKey;
  await act(async () => {
    resolveOld({
      project: { id: "old-p" },
      run: { id: "old-r", projectId: "old-p", status: "cancelled" },
    });
    await oldCheck;
  });
  const stateAfterOld = JSON.parse(
    sessionStorage.getItem("drama_creation_intent")!,
  );
  const phaseAfterOld = h.result.current.phase;
  await act(async () => {
    resolveCreate({ id: "new-p" });
    await newSubmit;
  });
  expect(stateAfterOld.requestKey).toBe(newKey);
  expect(phaseAfterOld).toBe("creating-project");
});

test("late abandoned context must not unlock the active recovery retry", async () => {
  sessionStorage.setItem(
    "drama_creation_intent",
    JSON.stringify({
      account: "u",
      session: getSessionScope(),
      requestKey: "legacy-alias",
      draft,
      projectId: "old-p",
      phase: "start-unknown",
      error: "",
    }),
  );
  let resolveOld!: (v: unknown) => void;
  let resolveNew!: (v: unknown) => void;
  const get = vi
    .spyOn(api, "get")
    .mockImplementationOnce(
      () =>
        new Promise((r) => {
          resolveOld = r;
        }),
    )
    .mockImplementation(
      () =>
        new Promise((r) => {
          resolveNew = r;
        }),
    );
  const post = vi
    .spyOn(api, "post")
    .mockImplementation((path) =>
      path === "/projects"
        ? Promise.resolve({ id: "new-p" })
        : Promise.reject(new Error("response lost")),
    );
  const h = renderHook(() => useCreateIntent());
  let oldCheck!: Promise<void>;
  act(() => {
    oldCheck = h.result.current.check();
  });
  act(() => h.result.current.clear());
  await act(async () => {
    await h.result.current.submit(draft);
  });
  expect(h.result.current.phase).toBe("start-unknown");
  let newCheck!: Promise<void>;
  act(() => {
    newCheck = h.result.current.check();
  });
  await act(async () => {
    resolveOld(null);
    await oldCheck;
  });
  await act(async () => {
    await h.result.current.retrySameIntent();
  });
  expect(get).toHaveBeenCalledTimes(2);
  expect(post).toHaveBeenCalledTimes(2);
  await act(async () => {
    resolveNew(null);
    await newCheck;
  });
});

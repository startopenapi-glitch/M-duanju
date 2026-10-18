import { test, expect, vi, afterEach, beforeEach } from "vitest";
import { renderHook, act, cleanup } from "@testing-library/react";
import { api, getSessionScope } from "../../lib/api";
import { useProjectIntent } from "./useProjectIntent";
const scope = { totalSeconds: 15, aspect: "16:9" };
beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  localStorage.setItem("drama_session_revision", "same-session");
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
test("legacy query from unmounted project cannot erase new intent after clear on remounted page", async () => {
  const slot = `drama_project_intent:${getSessionScope()}:p`;
  sessionStorage.setItem(
    slot,
    JSON.stringify({
      key: "legacy-alias",
      mode: "first",
      scope,
      projectId: "p",
    }),
  );
  let resolveOld!: (v: unknown) => void;
  vi.spyOn(api, "get").mockImplementation(
    () =>
      new Promise((r) => {
        resolveOld = r;
      }),
  );
  let rejectNew!: (v: unknown) => void;
  const post = vi.spyOn(api, "post").mockImplementation(
    () =>
      new Promise((_, r) => {
        rejectNew = r;
      }),
  );
  const first = renderHook(() => useProjectIntent("p"));
  let oldCheck!: Promise<unknown>;
  act(() => {
    oldCheck = first.result.current("first", scope);
  });
  first.unmount();
  const second = renderHook(() => useProjectIntent("p"));
  act(() => second.result.current.clearLegacyPending());
  let newSubmit!: Promise<unknown>;
  act(() => {
    newSubmit = second.result.current("first", scope).catch(() => {});
  });
  const newKey = JSON.parse(sessionStorage.getItem(slot)!).key;
  await act(async () => {
    resolveOld({
      project: { id: "p" },
      run: { id: "old-r", projectId: "p", status: "cancelled" },
    });
    await oldCheck;
  });
  await act(async () => {
    rejectNew(new Error("response lost"));
    await newSubmit;
  });
  expect(post).toHaveBeenCalledTimes(1);
  expect(JSON.parse(sessionStorage.getItem(slot) || "null")?.key).toBe(newKey);
});

for (const outcome of ["success", "known-rejection"]) {
  test(`late POST ${outcome} from unmounted project cannot clear the replacement intent`, async () => {
    const { ApiError } = await import("../../lib/api");
    const slot = `drama_project_intent:${getSessionScope()}:p`;
    let resolveOld!: (v: unknown) => void,
      rejectOld!: (v: unknown) => void,
      rejectNew!: (v: unknown) => void;
    const post = vi
      .spyOn(api, "post")
      .mockImplementationOnce(
        () =>
          new Promise((r, j) => {
            resolveOld = r;
            rejectOld = j;
          }),
      )
      .mockImplementation(
        () =>
          new Promise((_, j) => {
            rejectNew = j;
          }),
      );
    vi.spyOn(api, "get").mockResolvedValue({
      project: { id: "p" },
      run: { id: "old-r", projectId: "p", status: "cancelled" },
    });
    const first = renderHook(() => useProjectIntent("p"));
    let oldSubmit!: Promise<unknown>;
    act(() => {
      oldSubmit = first.result.current("first", scope).catch(() => {});
    });
    first.unmount();
    const second = renderHook(() => useProjectIntent("p"));
    await act(async () => {
      await second.result.current("first", scope);
    });
    let newSubmit!: Promise<unknown>;
    act(() => {
      newSubmit = second.result.current("first", scope).catch(() => {});
    });
    const newKey = JSON.parse(sessionStorage.getItem(slot)!).key;
    await act(async () => {
      if (outcome === "success") resolveOld({ run: { id: "old-r" } });
      else rejectOld(new ApiError("rejected", { code: "run_not_created" }));
      await oldSubmit;
    });
    expect(JSON.parse(sessionStorage.getItem(slot) || "null")?.key).toBe(
      newKey,
    );
    await act(async () => {
      rejectNew(new Error("response lost"));
      await newSubmit;
    });
    expect(post).toHaveBeenCalledTimes(2);
    expect(JSON.parse(sessionStorage.getItem(slot) || "null")?.key).toBe(
      newKey,
    );
  });
}

test("late versioned context null from unmounted project cannot post a cleared intent", async () => {
  const slot = `drama_project_intent:${getSessionScope()}:p`;
  sessionStorage.setItem(
    slot,
    JSON.stringify({
      key: "old-key",
      mode: "first",
      scope,
      projectId: "p",
      runIdentityVersion: 1,
    }),
  );
  let resolveOld!: (v: unknown) => void, rejectNew!: (v: unknown) => void;
  vi.spyOn(api, "get")
    .mockImplementationOnce(
      () =>
        new Promise((r) => {
          resolveOld = r;
        }),
    )
    .mockResolvedValue({
      project: { id: "p" },
      run: { id: "old-r", projectId: "p", status: "cancelled" },
    });
  const post = vi.spyOn(api, "post").mockImplementation(
    () =>
      new Promise((_, j) => {
        rejectNew = j;
      }),
  );
  const first = renderHook(() => useProjectIntent("p"));
  let oldCheck!: Promise<unknown>;
  act(() => {
    oldCheck = first.result.current("first", scope);
  });
  first.unmount();
  const second = renderHook(() => useProjectIntent("p"));
  await act(async () => {
    await second.result.current("first", scope);
  });
  let newSubmit!: Promise<unknown>;
  act(() => {
    newSubmit = second.result.current("first", scope).catch(() => {});
  });
  const newKey = JSON.parse(sessionStorage.getItem(slot)!).key;
  await act(async () => {
    resolveOld(null);
    await oldCheck;
  });
  expect(post).toHaveBeenCalledTimes(1);
  await act(async () => {
    rejectNew(new Error("response lost"));
    await newSubmit;
  });
  expect(JSON.parse(sessionStorage.getItem(slot) || "null")?.key).toBe(newKey);
});

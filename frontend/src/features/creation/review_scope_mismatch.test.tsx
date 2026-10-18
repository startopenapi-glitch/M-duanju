import { test, expect, vi, afterEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { api, getSessionScope } from "../../lib/api";
import { useProjectIntent } from "./useProjectIntent";

afterEach(() => { vi.restoreAllMocks(); sessionStorage.clear(); });

test("REVIEW: a found original run must not silently replace a newly changed instruction", async () => {
  const key = "review-original-key";
  sessionStorage.setItem(
    `drama_project_intent:${getSessionScope()}:p`,
    JSON.stringify({ key, mode: "revise", scope: { instruction: "old instruction" }, projectId: "p", runIdentityVersion: 1 }),
  );
  vi.spyOn(api, "get").mockResolvedValue({
    project: { id: "p" },
    run: { id: "r", projectId: "p", status: "cancelled" },
  });
  const post = vi.spyOn(api, "post").mockResolvedValue({});
  const h = renderHook(() => useProjectIntent("p"));
  let outcome: unknown;
  await act(async () => {
    try { outcome = await h.result.current("revise", { instruction: "new instruction" }); }
    catch (e) { outcome = e; }
  });
  // A new instruction is a new request. It must not be silently reported as the old run,
  // and the pending original key must not be discarded as if the new request had succeeded.
  expect(post).not.toHaveBeenCalled();
  expect(outcome).toBeInstanceOf(Error);
  expect(sessionStorage.getItem(`drama_project_intent:${getSessionScope()}:p`)).not.toBeNull();
});

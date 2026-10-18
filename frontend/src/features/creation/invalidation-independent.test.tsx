import { test, expect, vi, afterEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import { ProjectsProvider, useSharedProjects } from "./ProjectsProvider";
import { api } from "../../lib/api";
afterEach(() => vi.restoreAllMocks());
test("review: a business invalidation during the old list request must be followed by a fresh read", async () => {
  let finish!: (x: unknown) => void;
  let calls = 0;
  const get = vi.spyOn(api, "get").mockImplementation(async () => {
    calls++;
    if (calls === 1)
      return new Promise((resolve) => {
        finish = resolve;
      });
    return { items: [{ id: "p", title: "fresh" }], total: 1 };
  });
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <ProjectsProvider>{children}</ProjectsProvider>
  );
  const h = renderHook(() => useSharedProjects("review-user"), { wrapper });
  await waitFor(() => expect(finish).toBeTypeOf("function"));
  act(() => window.dispatchEvent(new Event("drama:projects-changed")));
  await act(async () =>
    finish({ items: [{ id: "p", title: "stale-before-recharge" }], total: 1 }),
  );
  expect(h.result.current.items[0]?.title).toBe("fresh");
  expect(get).toHaveBeenCalledTimes(2);
  h.unmount();
});

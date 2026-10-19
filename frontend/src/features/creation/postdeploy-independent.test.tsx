import { test, expect, vi, afterEach } from "vitest";
import {
  renderHook,
  render,
  screen,
  act,
  waitFor,
} from "@testing-library/react";
import { api } from "../../lib/api";
import { ProjectsProvider, useSharedProjects } from "./ProjectsProvider";
import { useProjectRun } from "./useProjectRun";
import { presentProject } from "./projectPresentation";
afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});
function List() {
  const data = useSharedProjects("review-user");
  return (
    <div>
      {data.items.map((p) => (
        <span key={p.id}>
          {p.title}:{p.status}
        </span>
      ))}
    </div>
  );
}
test("review: returning from completed project revalidates cached list status", async () => {
  let current = "SHOTS_GENERATING";
  const get = vi
    .spyOn(api, "get")
    .mockImplementation(async () => ({
      items: [{ id: "p", title: "review-project", status: current }],
      total: 1,
    }));
  const h = render(
    <ProjectsProvider>
      <List />
    </ProjectsProvider>,
  );
  await screen.findByText("review-project:SHOTS_GENERATING");
  h.rerender(
    <ProjectsProvider>
      <div>project-detail</div>
    </ProjectsProvider>,
  );
  current = "COMPLETED";
  h.rerender(
    <ProjectsProvider>
      <List />
    </ProjectsProvider>,
  );
  await act(async () => {});
  expect(screen.getByText("review-project:COMPLETED")).toBeTruthy();
  expect(get).toHaveBeenCalledTimes(2);
});
test("review: visibility restore rechecks project list changed in another tab", async () => {
  let current = "waiting";
  const get = vi
    .spyOn(api, "get")
    .mockImplementation(async () => ({
      items: [{ id: "p", title: current, status: "DRAFT" }],
      total: 1,
    }));
  const h = render(
    <ProjectsProvider>
      <List />
    </ProjectsProvider>,
  );
  await screen.findByText("waiting:DRAFT");
  const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(true);
  act(() => document.dispatchEvent(new Event("visibilitychange")));
  current = "finished";
  hidden.mockReturnValue(false);
  await act(async () => document.dispatchEvent(new Event("visibilitychange")));
  expect(screen.getByText("finished:DRAFT")).toBeTruthy();
  expect(get).toHaveBeenCalledTimes(2);
  h.unmount();
});
test("review: project initial read recovers when connection comes online", async () => {
  let available = false;
  const get = vi.spyOn(api, "get").mockImplementation(async (path) => {
    if (!available) throw new Error("offline");
    return path.endsWith("/runs/latest")
      ? { run: { id: "run", status: "running" } }
      : {
          id: "p",
          status: "SHOTS_GENERATING",
          shots: [],
          characters: [],
          scenes: [],
          reviews: [],
        };
  });
  const h = renderHook(() => useProjectRun("p"));
  await waitFor(() => expect(h.result.current.error).toBe("offline"));
  available = true;
  await act(async () => window.dispatchEvent(new Event("online")));
  expect(h.result.current.project?.id).toBe("p");
  expect(get).toHaveBeenCalledTimes(3);
  h.unmount();
});
test.each(["cancelled", "failed"] as const)(
  "review: terminal run %s has no active creation stage",
  (status) => {
    const view = presentProject(
      { status: "SHOTS_REVIEW", finalUrl: "", generationJobs: [] } as any,
      { run: { id: "r", status }, outputs: [] } as any,
      "ok",
    );
    expect(view.stageState).not.toBe("active");
  },
);

test("review: cache refresh fetches subscribed search only, not every abandoned search prefix", async () => {
  const get = vi.spyOn(api, "get").mockResolvedValue({ items: [], total: 0 });
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <ProjectsProvider>{children}</ProjectsProvider>
  );
  const h = renderHook(({ q }) => useSharedProjects("review-user", 0, { q }), {
    wrapper,
    initialProps: { q: "a" },
  });
  await waitFor(() => expect(h.result.current.loading).toBe(false));
  h.rerender({ q: "ab" });
  await waitFor(() => expect(h.result.current.loading).toBe(false));
  h.rerender({ q: "abc" });
  await waitFor(() => expect(h.result.current.loading).toBe(false));
  get.mockClear();
  await act(async () =>
    window.dispatchEvent(new Event("drama:projects-changed")),
  );
  expect(get.mock.calls.map(([url]) => url)).toEqual([
    "/projects?page=1&pageSize=20&q=abc",
  ]);
  h.unmount();
});

test("review: signed-out navigation does not query private projects", async () => {
  const get = vi.spyOn(api, "get").mockResolvedValue({ items: [], total: 0 });
  const h = renderHook(() => useSharedProjects(""));
  await act(async () => {});
  expect(get).not.toHaveBeenCalled();
  h.unmount();
});

import { ShotList } from "./ShotList";
test("review: changed shot output replaces the player source", () => {
  const shot = {
    id: "s",
    status: "GENERATED",
    videoUrl: "/old.mp4",
    frames: [],
  } as any;
  const h = render(<ShotList shots={[shot]} />);
  const old = h.container.querySelector("video");
  h.rerender(<ShotList shots={[{ ...shot, videoUrl: "/new.mp4" }]} />);
  expect(h.container.querySelector("video")).not.toBe(old);
  expect(h.container.querySelector("video source")?.getAttribute("src")).toBe(
    "/new.mp4",
  );
});

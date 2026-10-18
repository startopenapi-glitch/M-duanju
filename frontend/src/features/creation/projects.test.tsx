import { test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { api } from "../../lib/api";
import { useProjects, projectsChanged } from "../../lib/projects";
import { ProjectsProvider } from "./ProjectsProvider";
afterEach(() => vi.restoreAllMocks());
function List({ name }: { name: string }) {
  const r = useProjects("u");
  return (
    <section aria-label={name}>
      {r.items.map((p) => (
        <p key={p.id}>
          {name}-{p.id}
        </p>
      ))}
      <button onClick={r.more}>{name}-more</button>
      <button onClick={projectsChanged}>{name}-refresh</button>
    </section>
  );
}
test("home_and_rail_share_project_fetch", async () => {
  const get = vi
    .spyOn(api, "get")
    .mockResolvedValue({ items: [{ id: "one" }], total: 1 });
  render(
    <ProjectsProvider>
      <List name="home" />
      <List name="rail" />
    </ProjectsProvider>,
  );
  await screen.findByText("home-one");
  await screen.findByText("rail-one");
  expect(
    get.mock.calls.filter(([p]) => p.startsWith("/projects?")),
  ).toHaveLength(1);
});
test("refresh_preserves_loaded_pages", async () => {
  const get = vi
    .spyOn(api, "get")
    .mockImplementation(async (p) => ({
      items: [{ id: p.includes("page=2") ? "two" : "one" }],
      total: 21,
    }));
  render(
    <ProjectsProvider>
      <List name="home" />
    </ProjectsProvider>,
  );
  await screen.findByText("home-one");
  fireEvent.click(screen.getByText("home-more"));
  await screen.findByText("home-two");
  fireEvent.click(screen.getByText("home-refresh"));
  await waitFor(() =>
    expect(get.mock.calls.filter(([p]) => p.includes("page=2"))).toHaveLength(
      2,
    ),
  );
  expect(screen.getByText("home-two")).toBeTruthy();
});

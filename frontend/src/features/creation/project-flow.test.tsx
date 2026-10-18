import { test, expect, vi, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import ProjectPage from "../../pages/Project";
import { api } from "../../lib/api";
const p = {
  id: "p",
  title: "项目",
  idea: "test",
  aspect: "16:9",
  totalSeconds: 15,
  status: "FAILED",
  finalUrl: "",
  keyvisualUrl: "",
  spentCredits: 0,
  characters: [],
  scenes: [],
  shots: [],
  reviews: [],
  generationJobs: [],
};
const run = {
  id: "r",
  projectId: "p",
  projectVersion: 1,
  mode: "first",
  status: "failed",
  autoResume: true,
  creditsEstimated: 630,
  creditsSettled: 0,
};
afterEach(() => vi.restoreAllMocks());
function show(project: any, view: any) {
  vi.spyOn(api, "get").mockImplementation(async (path) =>
    path.endsWith("/runs/latest") ? view : project,
  );
  render(
    <MemoryRouter initialEntries={["/project/p"]}>
      <Routes>
        <Route path="/project/:id" element={<ProjectPage />} />
      </Routes>
    </MemoryRouter>,
  );
}
test("unresolved_job_blocks_blind_retry", async () => {
  show(
    {
      ...p,
      generationJobs: [
        {
          id: "j",
          kind: "shot",
          status: "reconcile",
          phase: "submitting",
          errorCode: "submission_unknown",
          manualRequired: true,
        },
      ],
    },
    { run, availableCredits: 0, shortfallCredits: 0 },
  );
  await screen.findByText("项目");
  expect(screen.queryByRole("button", { name: "继续自动生成" })).toBeNull();
  expect(screen.queryByRole("button", { name: "重新开始生成" })).toBeNull();
});
test("settlement_pending_is_not_zero_credit_shortfall", async () => {
  show(p, {
    run: { ...run, status: "settlement_pending" },
    availableCredits: 0,
    shortfallCredits: 0,
  });
  await screen.findByText("项目");
  expect(screen.queryByRole("link", { name: "充值并继续" })).toBeNull();
  expect(screen.queryByText(/还差/)).toBeNull();
});

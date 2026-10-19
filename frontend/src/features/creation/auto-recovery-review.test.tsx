import { test, expect, vi, afterEach } from "vitest";
import {
  render,
  screen,
  fireEvent,
  waitFor,
  cleanup,
} from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import ProjectPage from "../../pages/Project";
import { api } from "../../lib/api";
const project = {
  id: "p",
  title: "独立复审项目",
  idea: "test",
  aspect: "16:9",
  totalSeconds: 15,
  status: "FAILED",
  finalUrl: "",
  keyvisualUrl: "",
  spentCredits: 0,
  budgetCredits: 2000,
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
afterEach(() => {
  vi.restoreAllMocks();
  sessionStorage.clear();
  cleanup();
});
function show(p: any, status: string, reason = "", shortfall = 0) {
  vi.spyOn(api, "get").mockImplementation(async (path) =>
    path.endsWith("/runs/latest")
      ? {
          run: { ...run, status, waitingReason: reason },
          availableCredits: 100,
          shortfallCredits: shortfall,
        }
      : path.startsWith("/runs/context")
        ? null
        : p,
  );
  return render(
    <MemoryRouter initialEntries={["/project/p"]}>
      <Routes>
        <Route path="/project/:id" element={<ProjectPage />} />
      </Routes>
    </MemoryRouter>,
  );
}
for (const status of ["waiting_credits", "settlement_pending"])
  test(`review: ${status} run remains cancellable without paying`, async () => {
    show(
      project,
      status,
      status === "waiting_credits" ? "balance" : "settlement",
      status === "waiting_credits" ? 530 : 0,
    );
    await screen.findByRole("region", { name: "自动制作状态" });
    expect(
      screen.queryByRole("button", { name: "取消自动制作" }),
    ).not.toBeNull();
  });
test("review: explicit retry remains available for server-verified known failures", async () => {
  show(
    {
      ...project,
      status: "SHOTS_GENERATING",
      shots: [
        {
          id: "s",
          shotCode: "S01",
          seq: 1,
          duration: 5,
          status: "FAILED",
          videoUrl: "",
          frames: [],
          retryCount: 2,
          retryLimit: 2,
          errorMsg: "provider failed",
        },
      ],
    },
    "failed",
    "shot_failed",
  );
  await screen.findByRole("region", { name: "自动制作状态" });
  expect(screen.queryByRole("button", { name: "继续自动生成" })).not.toBeNull();
});
test("review: reload restores unresolved revision instruction and makes the same request recoverable", async () => {
  const p = {
    ...project,
    status: "COMPLETED",
    finalUrl: "/media/original.mp4",
  };
  const view = show(p, "completed");
  const post = vi
    .spyOn(api, "post")
    .mockRejectedValue(new TypeError("response lost"));
  await screen.findByLabelText("修改要求");
  fireEvent.change(screen.getByLabelText("修改要求"), {
    target: { value: "让主角抬头看向窗外" },
  });
  fireEvent.click(screen.getByRole("button", { name: "生成修改版" }));
  await screen.findByText("response lost");
  expect(post).toHaveBeenCalledTimes(1);
  view.unmount();
  render(
    <MemoryRouter initialEntries={["/project/p"]}>
      <Routes>
        <Route path="/project/:id" element={<ProjectPage />} />
      </Routes>
    </MemoryRouter>,
  );
  await screen.findByLabelText("修改要求");
  expect((screen.getByLabelText("修改要求") as HTMLTextAreaElement).value).toBe(
    "让主角抬头看向窗外",
  );
});
test("review: double cancellation dispatches one command and reloads cancelled state", async () => {
  let cancelled = false,
    finish!: () => void;
  vi.spyOn(api, "get").mockImplementation(async (path) =>
    path.endsWith("/runs/latest")
      ? {
          run: { ...run, status: cancelled ? "cancelled" : "running" },
          availableCredits: 100,
          shortfallCredits: 0,
        }
      : project,
  );
  const post = vi.spyOn(api, "post").mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = () => {
          cancelled = true;
          resolve({});
        };
      }),
  );
  render(
    <MemoryRouter initialEntries={["/project/p"]}>
      <Routes>
        <Route path="/project/:id" element={<ProjectPage />} />
      </Routes>
    </MemoryRouter>,
  );
  fireEvent.click(await screen.findByRole("button", { name: "取消自动制作" }));
  const button = screen.getByRole("button", { name: "确认取消" });
  fireEvent.click(button);
  fireEvent.click(button);
  expect(post).toHaveBeenCalledTimes(1);
  finish();
  await screen.findByText("全自动制作 · 已取消");
  expect(screen.queryByRole("button", { name: "确认取消" })).toBeNull();
});
test("review: cancelled jobs are not presented as queued work", async () => {
  show(
    {
      ...project,
      generationJobs: [
        {
          id: "j",
          kind: "shot",
          status: "cancelled",
          phase: "ready",
          errorCode: "run_cancelled",
          lastError: "执行已取消，任务未提交上游",
          manualRequired: false,
          attempts: 0,
          maxAttempts: 2,
          infrastructureAllowance: 4,
          reservedCredits: 0,
        },
      ],
    },
    "cancelled",
  );
  await screen.findByRole("region", { name: "自动制作状态" });
  expect(screen.queryByText(/任务 · 已排队/)).toBeNull();
});
test("review: finding a cancelled original intent must not announce a new generation", async () => {
  const format = await import("../../lib/format");
  const toast = vi.spyOn(format, "toast");
  let oldCancelled = false;
  vi.spyOn(api, "get").mockImplementation(async (path) =>
    path.endsWith("/runs/latest")
      ? {
          run: { ...run, status: oldCancelled ? "cancelled" : "failed" },
          availableCredits: 100,
          shortfallCredits: 0,
        }
      : path.startsWith("/runs/context")
        ? {
            run: { ...run, status: "cancelled" },
            project: { id: "p" },
            isLatest: true,
          }
        : project,
  );
  const post = vi
    .spyOn(api, "post")
    .mockRejectedValue(new TypeError("response lost"));
  render(
    <MemoryRouter initialEntries={["/project/p"]}>
      <Routes>
        <Route path="/project/:id" element={<ProjectPage />} />
      </Routes>
    </MemoryRouter>,
  );
  fireEvent.click(await screen.findByRole("button", { name: "继续自动生成" }));
  await screen.findByText("response lost");
  oldCancelled = true;
  fireEvent.click(screen.getByRole("button", { name: "继续自动生成" }));
  await screen.findByText("全自动制作 · 已取消");
  expect(post).toHaveBeenCalledTimes(1);
  expect(toast).not.toHaveBeenCalledWith("已开始全自动生成", "ok");
});
test("review: known upstream polling exhaustion exposes safe original-job recovery", async () => {
  show(
    {
      ...project,
      status: "SHOTS_GENERATING",
      generationJobs: [
        {
          id: "j",
          kind: "shot",
          status: "reconcile",
          phase: "polling",
          errorCode: "recovery_required",
          lastError: "查询上游超时",
          canResume: true,
          manualRequired: false,
          attempts: 6,
          maxAttempts: 2,
          infrastructureAllowance: 4,
          reservedCredits: 275,
          requiredCredits: 0,
          availableCredits: 0,
          shortfallCredits: 0,
        },
      ],
    },
    "running",
  );
  await screen.findByText("查询上游超时");
  expect(
    screen.queryByRole("button", { name: /恢复.*(?:查询|原任务)|继续查询/ }),
  ).not.toBeNull();
});

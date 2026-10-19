import { test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { OutputPreview } from "./OutputPreview";
afterEach(() => vi.restoreAllMocks());
test("download_failure_keeps_version_context", async () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response("", { status: 404 }),
  );
  render(
    <OutputPreview
      project={
        { id: "p", finalUrl: "/expired.mp4", status: "COMPLETED" } as any
      }
      runView={null}
      readState="empty"
    />,
  );
  fireEvent.click(screen.getByRole("link", { name: "下载成片" }));
  expect((await screen.findByRole("alert")).textContent).toContain("无法下载");
  expect(
    screen.getByRole("link", { name: "新窗口播放" }).getAttribute("href"),
  ).toBe("/expired.mp4");
});
test("expired_output_is_not_reported_as_download_success", async () => {
  const { container } = render(
    <OutputPreview
      project={
        { id: "p", finalUrl: "/expired.mp4", status: "COMPLETED" } as any
      }
      runView={null}
      readState="empty"
    />,
  );
  fireEvent.error(container.querySelector("video")!);
  expect((await screen.findByRole("alert")).textContent).toContain(
    "暂时无法加载",
  );
  expect(screen.queryByText("下载成功")).toBeNull();
});

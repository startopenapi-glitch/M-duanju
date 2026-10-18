import { test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Composer } from "./Composer";
const draft = {
  idea: "一个足够长的创作想法",
  aspect: "16:9" as const,
  totalSeconds: 15,
  filmReference: "教父式低调布光",
  mood: "克制、有张力",
};
test("starts_once_without_budget_modal", () => {
  const submit = vi.fn();
  render(
    <Composer
      draft={draft}
      onChange={() => {}}
      onSubmit={submit}
      estimate={{
        estimatedCredits: 630,
        resolution: "720P",
        segmentCount: 3,
        suggestedBudgetYuan: 10,
      }}
      busy={false}
      error=""
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "开始生成" }));
  expect(submit).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("button", { name: /确认预算/ })).toBeNull();
  expect(screen.getByText(/630/)).toBeTruthy();
});
test("cannot_submit_without_verified_estimate", () => {
  const submit = vi.fn();
  render(
    <Composer
      draft={draft}
      onChange={() => {}}
      onSubmit={submit}
      estimate={null}
      busy={false}
      error="估算不可用"
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "开始生成" }));
  expect(submit).not.toHaveBeenCalled();
  expect(screen.getByRole("alert").textContent).toContain("估算不可用");
});

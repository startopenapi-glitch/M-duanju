import { test, expect } from "vitest";
import { presentProject, STAGES } from "./projectPresentation";
import type { Project } from "../../lib/api";
test("shots_review_has_single_stage", () => {
  const p = {
    status: "SHOTS_REVIEW",
    generationJobs: [],
    finalUrl: "",
  } as unknown as Project;
  const d = presentProject(p, null, "empty");
  expect(STAGES.filter((s) => s.key === d.stage)).toHaveLength(1);
  expect(d.stage).toBe("shots");
});
test("run_read_error_is_not_legacy_current_success", () => {
  const p = {
    status: "COMPLETED",
    finalUrl: "old.mp4",
    generationJobs: [],
  } as unknown as Project;
  expect(presentProject(p, null, "error").output).toBe("previous");
});

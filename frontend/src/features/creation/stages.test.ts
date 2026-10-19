import { describe, expect, it } from "vitest";
import type { Project } from "../../lib/api";
import {
  currentStagePath,
  stageForProject,
  stagePath,
  stageState,
} from "./stages";

const base = {
  id: "p",
  title: "p",
  idea: "synthetic",
  aspect: "16:9",
  totalSeconds: 15,
  status: "DRAFT",
  budgetCredits: 1000,
  spentCredits: 0,
  keyvisualUrl: "",
  finalUrl: "",
  errorMsg: "",
  createdAt: "",
  updatedAt: "",
  characters: [],
  scenes: [],
  shots: [],
  reviews: [],
} as unknown as Project;

describe("manual stage navigation", () => {
  it("maps statuses to the current stage", () => {
    expect(stageForProject({ ...base, status: "DRAFT" })).toBe("script");
    expect(stageForProject({ ...base, status: "SCRIPT_REVIEW" })).toBe("script");
    expect(
      stageForProject({ ...base, status: "KEYVISUAL_REVIEW" }),
    ).toBe("keyvisual");
    expect(stageForProject({ ...base, status: "SHOTS_REVIEW" })).toBe("shots");
    expect(stageForProject({ ...base, status: "COMPLETED" })).toBe("final");
  });

  it("locks future stages and allows completed/current links", () => {
    const p = { ...base, status: "KEYVISUAL_REVIEW" };
    expect(stageState(p, "script")).toBe("done");
    expect(stageState(p, "keyvisual")).toBe("current");
    expect(stageState(p, "shots")).toBe("locked");
    expect(stageState(p, "final")).toBe("locked");
    expect(stagePath("p", "shots")).toBe("/project/p/shots");
    expect(currentStagePath(p)).toBe("/project/p/keyvisual");
  });

  it("keeps failed projects at the last paid artifact", () => {
    expect(
      stageForProject({
        ...base,
        status: "FAILED",
        keyvisualUrl: "http://127.0.0.1/kv.jpg",
      }),
    ).toBe("keyvisual");
    expect(
      stageForProject({
        ...base,
        status: "FAILED",
        shots: [{ videoUrl: "http://127.0.0.1/s.mp4" }],
      } as unknown as Project),
    ).toBe("shots");
  });
});

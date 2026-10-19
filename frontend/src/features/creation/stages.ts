import type { Project } from "../../lib/api";

export type StageKey = "script" | "keyvisual" | "shots" | "final";
export type StageState = "done" | "current" | "locked";

export const STAGE_ITEMS: ReadonlyArray<{ key: StageKey; label: string }> = [
  { key: "script", label: "剧本" },
  { key: "keyvisual", label: "定妆照" },
  { key: "shots", label: "镜头" },
  { key: "final", label: "成片" },
];

const STAGE_RANK: Record<StageKey, number> = {
  script: 0,
  keyvisual: 1,
  shots: 2,
  final: 3,
};

export function isStageKey(value: string | undefined): value is StageKey {
  return (
    value === "script" ||
    value === "keyvisual" ||
    value === "shots" ||
    value === "final"
  );
}

export function stageForStatus(status: string): StageKey {
  switch (status) {
    case "KEYVISUAL_GENERATING":
    case "KEYVISUAL_REVIEW":
      return "keyvisual";
    case "SHOTS_GENERATING":
    case "SHOTS_REVIEW":
      return "shots";
    case "ASSEMBLING":
    case "COMPLETED":
      return "final";
    default:
      return "script";
  }
}

export function stageForProject(project: Project): StageKey {
  // A failed project keeps whatever artifacts already exist. Do not send the
  // user back to script after a paid key visual or shot was already produced.
  if (project.status === "FAILED" || project.status === "BUDGET_EXCEEDED") {
    if (project.finalUrl) return "final";
    if ((project.shots ?? []).some((s) => !!s.videoUrl)) return "shots";
    if (project.keyvisualUrl) return "keyvisual";
    return "script";
  }
  return stageForStatus(project.status);
}

export function stagePath(projectID: string, stage: StageKey): string {
  return `/project/${projectID}/${stage}`;
}

export function currentStagePath(project: Project): string {
  return stagePath(project.id, stageForProject(project));
}

export function stageState(project: Project, stage: StageKey): StageState {
  const current = STAGE_RANK[stageForProject(project)];
  const target = STAGE_RANK[stage];
  if (target < current) return "done";
  if (target === current) return "current";
  return "locked";
}

export function stageLabel(stage: StageKey): string {
  return STAGE_ITEMS.find((x) => x.key === stage)?.label ?? stage;
}

import type { GenerationRun, Project } from "../../lib/api";
export type RunContext = {
  run: GenerationRun;
  project: Pick<Project, "id" | "title" | "status">;
  isLatest: boolean;
  hasUnresolvedSubmission: boolean;
  availableCredits: number;
  shortfallCredits: number;
  observedAt: string;
};
export type CreationDraft = {
  idea: string;
  aspect: "16:9" | "9:16";
  totalSeconds: number;
  filmReference: string;
  mood: string;
};

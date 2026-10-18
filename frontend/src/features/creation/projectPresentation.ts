import type { Project, RunView } from "../../lib/api";
import type { BadgeTone } from "../../components/ui/StatusBadge";
export const STAGES = [
  { key: "script", label: "剧本" },
  { key: "keyvisual", label: "定妆照" },
  { key: "shots", label: "镜头" },
  { key: "assembly", label: "成片" },
] as const;
export type ProjectAction =
  | "refresh"
  | "topup"
  | "cancel"
  | "authorize-new"
  | "revise"
  | "download";
export function presentProject(
  p: Project,
  rv: RunView | null,
  read: "loading" | "ok" | "empty" | "error",
) {
  const stage =
    (
      {
        SCRIPT_GENERATING: "script",
        SCRIPT_REVIEW: "script",
        KEYVISUAL_GENERATING: "keyvisual",
        KEYVISUAL_REVIEW: "keyvisual",
        SHOTS_GENERATING: "shots",
        SHOTS_REVIEW: "shots",
        ASSEMBLING: "assembly",
        COMPLETED: "assembly",
      } as const
    )[p.status as "COMPLETED"] ?? null;
  const run = rv?.run,
    unknown =
      p.generationJobs?.some(
        (j) =>
          j.errorCode === "submission_unknown" ||
          (j.manualRequired && j.phase === "submitting"),
      ) ?? false;
  const currentOutput =
    (read === "ok" || read === "empty") &&
    !!p.finalUrl &&
    p.status === "COMPLETED" &&
    (!run ||
      (run.status === "completed" &&
        ((rv?.outputs ?? []).some(
          (o) => o.runId === run.id && o.url === p.finalUrl,
        ) ||
          !rv?.outputs)));
  let executionLabel = "尚未开始",
    tone: BadgeTone = "neutral",
    notice = "",
    actions: ProjectAction[] = ["refresh"];
  if (read === "error") {
    executionLabel = "执行状态暂不可用";
    notice = "请重试查询，核实原执行后再操作";
    tone = "danger";
  } else if (unknown) {
    executionLabel = "提交结果待核对";
    notice = "保留原任务与预占积分，正在核对上游结果。不要重复生成。";
    tone = "warning";
    if (run?.status === "cancelled") notice += " 已停止派发新任务。";
  } else if (run) {
    const labels = {
      queued: "已排队",
      running: "制作中",
      waiting_credits: "等待积分",
      settlement_pending: "结算核对中",
      completed: "已完成",
      failed: "已停止",
      cancelled: "已取消",
    };
    executionLabel = labels[run.status];
    tone =
      run.status === "completed"
        ? "success"
        : ["failed", "cancelled"].includes(run.status)
          ? "danger"
          : ["waiting_credits", "settlement_pending"].includes(run.status)
            ? "warning"
            : "info";
    if (
      ["queued", "running", "waiting_credits", "settlement_pending"].includes(
        run.status,
      )
    )
      actions.push("cancel");
    if (
      run.status === "waiting_credits" &&
      run.autoResume &&
      (rv?.shortfallCredits ?? 0) > 0
    )
      actions.push("topup");
    if (run.status === "settlement_pending")
      notice = "正在核对已发生的费用与交付结果，请勿重复提交。";
  }
  if (currentOutput) {
    actions.push("download");
    if (
      !unknown &&
      (!run || ["completed", "failed", "cancelled"].includes(run.status))
    )
      actions.push("revise");
  }
  if (read === "empty" && !unknown && !currentOutput)
    actions.push("authorize-new");
  return {
    stage,
    stageState: (currentOutput
      ? "complete"
      : !stage
        ? "unknown"
        : read === "error" ||
            read === "loading" ||
            unknown ||
            (run && !["queued", "running"].includes(run.status))
          ? "paused"
          : "active") as "complete" | "paused" | "active" | "unknown",
    executionLabel,
    tone,
    notice,
    actions,
    output: (currentOutput
      ? "current"
      : p.finalUrl
        ? "previous"
        : "missing") as "current" | "previous" | "missing",
  };
}

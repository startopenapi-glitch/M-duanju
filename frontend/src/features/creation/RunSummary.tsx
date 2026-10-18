import { Link } from "react-router-dom";
import { api, type Project, type RunView } from "../../lib/api";
const RUN_LABEL: Record<string, string> = {
  queued: "排队中",
  running: "制作中",
  waiting_credits: "等待充值",
  settlement_pending: "待结算",
  completed: "已完成",
  failed: "已停止",
  cancelled: "已取消",
};
const REASON_LABEL: Record<string, string> = {
  balance: "积分不足",
  script_failed: "剧本生成失败",
  keyvisual_error: "定妆照提交失败",
  shots_error: "分镜派发失败",
  assemble_error: "合成提交失败",
  shot_failed: "有镜头生成失败",
  assemble_failed: "合成失败",
  budget_insufficient: "超出服务端金额上限",
  project_failed: "项目已失败",
  account_inactive: "账号已停用",
  project_changed: "项目归属已变化",
  project_missing: "项目已删除",
  revision_plan_invalid: "修改方案已失效，请重新填写修改要求",
  superseded: "已被新版本替代",
  no_estimate: "缺少费用依据",
  stage_loop: "阶段推进异常",
  unknown_stage: "未知阶段",
  auto_resume_off: "未授权自动续跑",
  final_missing: "成片文件缺失",
};
type Props = {
  p: Project;
  runView: RunView | null;
  runState: "loading" | "ok" | "empty" | "error";
  runError: string;
  busy: string;
  canRevise: boolean;
  unknownSubmission: boolean;
  cancelConfirm: boolean;
  setCancelConfirm: (v: boolean) => void;
  startAuto: () => void;
  load: () => Promise<void>;
  act: (key: string, fn: () => Promise<unknown>, msg?: string) => Promise<void>;
};
export function RunSummary({
  p,
  runView,
  runState,
  runError,
  busy,
  canRevise,
  unknownSubmission,
  cancelConfirm,
  setCancelConfirm,
  startAuto,
  load,
  act,
}: Props) {
  const run = runState === "ok" ? runView?.run : undefined,
    id = p.id;
  return (
    <>
      {unknownSubmission && (
        <div role="alert" className="note note-info">
          提交结果待核对，已保留原任务与预占积分，请勿重复生成。
        </div>
      )}
      {run ? (
        <section className="card space-y-2" aria-label="自动制作状态">
          <div className="flex items-center justify-between gap-3 flex-wrap">
            <b>全自动制作 · {RUN_LABEL[run.status] || run.status}</b>
            <span className="text-xs text-ink3">
              执行 v{run.projectVersion} · 预计 {run.creditsEstimated} 积分 ·
              当前可用 {runView?.availableCredits ?? 0} 积分
            </span>
          </div>
          {run.status === "waiting_credits" ||
          run.status === "settlement_pending" ? (
            <div className="note note-info">
              {run.status === "settlement_pending" ? (
                <p>
                  生成结果已保留，正在核对结算。请等待原任务核对，不要重复生成。
                </p>
              ) : (
                <>
                  {run.waitingReason === "settlement" && (
                    <p>生成结果已保留，等待结算；补足积分后继续原任务。</p>
                  )}
                  <p>
                    积分不足，还差 <b>{runView?.shortfallCredits ?? 0}</b>{" "}
                    积分。已完成素材会保留，到账后按原任务状态继续。
                  </p>
                  {run.autoResume &&
                    (runView?.shortfallCredits ?? 0) > 0 &&
                    !unknownSubmission && (
                      <Link
                        className="btn btn-primary btn-sm mt-2"
                        to={`/credits?runId=${run.id}`}
                      >
                        充值并继续
                      </Link>
                    )}
                </>
              )}
            </div>
          ) : run.status === "failed" ? (
            <div className="note note-err">
              自动制作已停止：
              {REASON_LABEL[run.waitingReason || ""] ||
                run.waitingReason ||
                "阶段失败"}
              。已产生的素材和积分流水保留；如仍要继续，可重新授权一次。
              {!canRevise && !unknownSubmission && (
                <button
                  className="btn btn-sm mt-2"
                  disabled={!!busy}
                  onClick={startAuto}
                >
                  继续自动生成
                </button>
              )}
            </div>
          ) : run.status === "completed" ? (
            <p className="text-sm text-ink2">
              制作完成，可在成片区下载。填写修改要求，即可生成新版本，原版素材与账单会保留。
            </p>
          ) : run.status === "cancelled" ? (
            <div className="note note-info">
              已停止自动推进；已提交的上游任务仍会核对和结算，不会继续派发新任务。
              {!canRevise && !unknownSubmission && (
                <button
                  className="btn btn-sm mt-2"
                  disabled={!!busy}
                  onClick={startAuto}
                >
                  重新开始生成
                </button>
              )}
            </div>
          ) : (
            <>
              <p className="text-sm text-ink2">
                系统正在自动推进：
                {p.status.endsWith("_GENERATING")
                  ? "生成中"
                  : p.status === "ASSEMBLING"
                    ? "合成中"
                    : "排队中"}
                。浏览器关闭不影响服务端任务。
              </p>
            </>
          )}
          {[
            "queued",
            "running",
            "waiting_credits",
            "settlement_pending",
          ].includes(run.status) && (
            <>
              {!cancelConfirm ? (
                <button
                  className="btn btn-sm"
                  disabled={!!busy}
                  onClick={() => setCancelConfirm(true)}
                >
                  取消自动制作
                </button>
              ) : (
                <div className="flex gap-2 items-center flex-wrap">
                  <span className="text-sm">
                    确认停止继续派发新任务？已产生的费用和素材不会撤回。
                  </span>
                  <button
                    className="btn btn-sm btn-danger"
                    disabled={!!busy}
                    onClick={() =>
                      act(
                        "cancel-run",
                        async () => {
                          await api.post(
                            `/projects/${id}/runs/${run.id}/cancel`,
                          );
                          setCancelConfirm(false);
                        },
                        "已停止自动推进",
                      )
                    }
                  >
                    确认取消
                  </button>
                  <button
                    className="btn btn-sm"
                    onClick={() => setCancelConfirm(false)}
                  >
                    返回
                  </button>
                </div>
              )}
            </>
          )}
        </section>
      ) : runState === "empty" && canRevise ? (
        <div className="note note-info">
          历史成片可直接下载，也可以填写修改要求生成新版本。
        </div>
      ) : runState === "empty" && !unknownSubmission ? (
        <section className="card space-y-2" aria-label="继续自动生成">
          <b>历史项目</b>
          <p className="text-sm text-ink2">
            该项目创建于全自动流程之前，不会在升级后自动消费。确认要继续剩余制作时，点一次下方按钮授权。
          </p>
          <button
            className="btn btn-primary btn-sm"
            disabled={!!busy}
            onClick={startAuto}
          >
            {busy === "start-run" ? (
              <>
                <span className="spin" /> 提交中
              </>
            ) : (
              "继续自动生成"
            )}
          </button>
        </section>
      ) : runState === "error" ? (
        <div role="alert" className="note note-err">
          <b>执行状态查询失败，暂时不能判断是历史项目还是自动制作。</b>
          <p className="text-sm">
            {runError || "服务暂不可用"}
            。为避免误触发生成，依赖执行状态的写操作已关闭。
          </p>
          <button className="btn btn-sm mt-2" onClick={() => load()}>
            重试查询
          </button>
        </div>
      ) : (
        <div role="status" className="note note-info">
          {unknownSubmission
            ? "原提交正在核对，暂不允许重复生成。"
            : "正在读取执行状态…"}
        </div>
      )}
    </>
  );
}

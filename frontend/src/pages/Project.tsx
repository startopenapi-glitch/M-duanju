import {
  useProjectIntent,
  pendingRevisionInstruction,
} from "../features/creation/useProjectIntent";
import { useProjectRun } from "../features/creation/useProjectRun";
import { RunSummary } from "../features/creation/RunSummary";
import { OutputPreview } from "../features/creation/OutputPreview";
import { ShotList } from "../features/creation/ShotList";
import { RevisionComposer } from "../features/creation/RevisionComposer";
import {
  presentProject,
  STAGES,
} from "../features/creation/projectPresentation";
import { useEffect, useState, useCallback, useRef } from "react";
import { Link, useParams } from "react-router-dom";
import {
  api,
  useSessionScope,
  ApiError,
  errorMessage,
  staleError,
  type Project,
  type RunView,
} from "../lib/api";
import { projectsChanged } from "../lib/projects";
import {
  STATUS_LABEL,
  SHOT_STATUS,
  tagCls,
  creditsToYuan,
  toast,
} from "../lib/format";

function newKey() {
  try {
    return crypto.randomUUID();
  } catch {
    return "k" + Date.now() + Math.random().toString(36).slice(2);
  }
}
export default function ProjectPage() {
  const { id = "" } = useParams();
  const sessionScope = useSessionScope();
  const {
    project: p,
    runView,
    readState: runState,
    runError,
    busy,
    error,
    actionError,
    runActive,
    refresh: load,
    act,
  } = useProjectRun(id);
  const [legacyClearConfirm, setLegacyClearConfirm] = useState(false);
  const [cancelConfirm, setCancelConfirm] = useState(false),
    [revisionInstruction, setRevisionInstruction] = useState("");
  const submitRun = useProjectIntent(id);
  useEffect(() => {
    setCancelConfirm(false);
    setLegacyClearConfirm(false);
    setRevisionInstruction(pendingRevisionInstruction(id));
  }, [id, sessionScope]);
  if (!p || p.id !== id)
    return (
      <div className="card" role={error ? "alert" : "status"}>
        {error ? (
          <>
            项目加载失败：{error}
            <button className="btn" onClick={() => void load()}>
              重试
            </button>
          </>
        ) : (
          "载入中…"
        )}
      </div>
    );
  const unknownSubmission =
    p.generationJobs?.some(
      (j) =>
        j.errorCode === "submission_unknown" ||
        (j.manualRequired && j.phase === "submitting"),
    ) ?? false;

  const run = runState === "ok" ? runView?.run : undefined;
  const auto = !!run;
  const legacy = runState === "empty";
  const startAuto = () =>
    act(
      "start-run",
      async () => {
        return await submitRun("first", {
          totalSeconds: p.totalSeconds,
          aspect: p.aspect,
        });
      },
      (result) =>
        result === "recovered"
          ? "已核对原执行，请查看最新状态"
          : "已提交自动生成，请查看执行状态",
    );
  const canRevise =
    !unknownSubmission &&
    (runState === "ok" || runState === "empty") &&
    !runActive &&
    p.status === "COMPLETED" &&
    !!p.finalUrl;
  const startRevision = () =>
    act(
      "revise-run",
      () =>
        submitRun("revise", {
          instruction: revisionInstruction.trim(),
          totalSeconds: p.totalSeconds,
          aspect: p.aspect,
        }),
      (result) =>
        result === "recovered"
          ? "已核对原执行，请查看最新状态"
          : "已提交修改版，请查看执行状态",
    );
  const jobs = p.generationJobs ?? [];
  const shots = p.shots ?? [];
  const chars = p.characters ?? [];
  const approved = shots.filter(
    (s) => s.status === "APPROVED" || s.status === "GENERATED",
  ).length;
  const generated = shots.filter((s) => s.videoUrl).length;
  const spent = creditsToYuan(p.spentCredits);

  return (
    <div className="space-y-6">
      {/* 头部 */}
      <div className="flex items-start justify-between gap-4 flex-wrap">
        <div>
          <h1 className="text-2xl font-bold">{p.title}</h1>
          <p className="text-sm text-ink3 mt-1 max-w-2xl">{p.idea}</p>
        </div>
        <div className="text-right">
          <span className={`tag ${tagCls(p.status)}`}>
            {STATUS_LABEL[p.status] || p.status}
          </span>
          <div className="text-[11px] text-ink3 mt-1">
            {p.aspect} · {p.totalSeconds}秒 · {p.shots.length} 段
          </div>
        </div>
      </div>

      <ol className="card ui-stages" aria-label="制作阶段">
        {STAGES.map((s, i) => {
          const d = presentProject(p, runView, runState),
            current = d.stage === s.key && d.stageState === "active";
          return (
            <li key={s.key} aria-current={current ? "step" : undefined}>
              <span className={current ? "ui-stage-active" : ""}>
                {d.stageState === "complete" ? "✓" : i + 1}
              </span>
              {s.label}
            </li>
          );
        })}
      </ol>

      <RunSummary
        {...{
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
        }}
      />
      <OutputPreview project={p} runView={runView} readState={runState} />
      <RevisionComposer
        {...{
          canRevise,
          revisionInstruction,
          setRevisionInstruction,
          busy,
          startRevision,
        }}
      />
      {error && (
        <div role="alert" className="note note-err">
          刷新失败，当前显示的是上次数据：{error}
          <button className="btn" onClick={() => load()}>
            重试
          </button>
        </div>
      )}
      {actionError && (
        <div role="alert" className="note note-err">
          {actionError.message}
          {actionError.requiredCredits > 0 && (
            <p>
              需要 ¥{creditsToYuan(actionError.requiredCredits)}，可用 ¥
              {creditsToYuan(Math.max(0, actionError.availableCredits))}，差额 ¥
              {creditsToYuan(
                Math.max(
                  0,
                  actionError.requiredCredits - actionError.availableCredits,
                ),
              )}
              。
              {actionError.code === "balance_insufficient"
                ? "请充值后重试。"
                : "请刷新后重试。"}
            </p>
          )}
        </div>
      )}
      {submitRun.hasPending && (
        <div className="note note-info" role="status">
          <p>
            {submitRun.hasLegacyPending
              ? "有旧版提交等待核对。无法找到原执行时，不会自动重新生成。"
              : "有上次修改等待核对。结束本机恢复只清理本地记录，不会取消服务端任务。"}
          </p>
          {legacyClearConfirm ? (
            <>
              <p>
                只结束本机恢复记录，不取消服务端任务、不扣费。请先核对项目状态，再决定是否重新生成。
              </p>
              <button
                className="btn"
                disabled={!!busy}
                onClick={() => {
                  submitRun.clearPending();
                  setLegacyClearConfirm(false);
                  void load();
                }}
              >
                确认结束本机恢复
              </button>
              <button
                className="btn"
                onClick={() => setLegacyClearConfirm(false)}
              >
                保留记录
              </button>
            </>
          ) : (
            <button
              className="btn"
              disabled={!!busy}
              onClick={() => setLegacyClearConfirm(true)}
            >
              结束本机恢复
            </button>
          )}
        </div>
      )}
      {(p.generationJobs ?? [])
        .filter((j) => j.status !== "done")
        .map((j) => (
          <div className="card space-y-2" key={j.id}>
            <b>
              {j.kind === "shot"
                ? "分镜"
                : j.kind === "keyvisual"
                  ? "定妆照"
                  : "合成"}
              任务 ·{" "}
              {j.status === "reconcile"
                ? "待恢复"
                : j.status === "cancelled"
                  ? "已取消"
                  : j.status === "failed"
                    ? "失败"
                    : j.status === "running"
                      ? "处理中"
                      : "已排队"}
            </b>
            <p className="text-sm">
              {j.manualRequired
                ? "提交结果未知，已保留任务与预占，请联系管理员核对，勿重复提交。"
                : j.lastError || "正在处理，请耐心等待。"}
            </p>
            {j.canResume &&
              !j.manualRequired &&
              j.phase === "polling" &&
              j.errorCode === "recovery_required" && (
                <button
                  className="btn btn-sm"
                  disabled={!!busy}
                  onClick={() =>
                    void act(
                      "resume-job",
                      () => api.post(`/projects/${id}/jobs/${j.id}/resume`),
                      "已安排原任务查询，不会重新生成",
                    )
                  }
                >
                  恢复原任务查询
                </button>
              )}
            {j.requiredCredits > 0 && (
              <p>
                本任务需要 ¥{creditsToYuan(j.requiredCredits)}，当前可用 ¥
                {creditsToYuan(j.availableCredits)}，差额 ¥
                {creditsToYuan(j.shortfallCredits)}。
              </p>
            )}
            <p className="text-xs text-ink3">
              已记录失败 {j.attempts} 次；基础上限 {j.maxAttempts}{" "}
              次，基础设施故障额外上限 {j.infrastructureAllowance} 次。
            </p>
          </div>
        ))}
      {p.status === "BUDGET_EXCEEDED" && (
        <div className="note note-err">
          <b>该项目停在旧版「预算熔断」状态。</b>已结算 ¥{spent}
          。点击上方「继续自动生成」会由服务端按本次范围重新授权上限，不再要求客户填写预算。
        </div>
      )}
      {p.status === "FAILED" && (
        <div className="note note-err">
          <b>流程失败：</b>
          {p.errorMsg || "未知错误"}
        </div>
      )}

      <details className="creation-details">
        <summary>查看剧本、角色与镜头详情</summary>
        {/* ═══ 剧本 ═══ */}
        {["DRAFT", "SCRIPT_REVIEW", "SCRIPT_GENERATING"].includes(p.status) && (
          <section>
            <h3 className="font-bold mb-3">① 剧本</h3>
            {p.characters.length === 0 ? (
              <div className="note note-info">剧本尚未生成。</div>
            ) : (
              <>
                {(p.characters ?? []).map((c) => (
                  <div
                    key={c.id}
                    className="card mb-3 border-l-[3px] border-l-amber"
                  >
                    <div className="flex items-center gap-2 mb-3">
                      <span className="tag tag-navy">{c.code}</span>
                      <b>{c.name}</b>
                      <span className="text-xs text-ink3">{c.ageRange}</span>
                    </div>
                    <div className="grid md:grid-cols-2 gap-3 text-sm">
                      <Field label="外形锚点" v={c.appearance} />
                      <Field label="服装" v={c.wardrobe} />
                      <Field label="性格" v={c.persona} />
                      <Field label="声线" v={c.voiceProfile} />
                    </div>
                  </div>
                ))}
                <h4 className="font-semibold text-sm mt-5 mb-2">
                  分镜 {p.shots.length} 段
                </h4>
                <div className="grid gap-2">
                  {(p.shots ?? []).map((s) => (
                    <div key={s.id} className="card py-3">
                      <div className="flex items-center justify-between gap-3 flex-wrap">
                        <div className="font-semibold text-sm">
                          {s.shotCode}
                          <span className="text-xs text-ink3 font-normal ml-2">
                            {s.shotSize} · {s.cameraHeight} · {s.cameraMove}
                          </span>
                        </div>
                        {s.dialogue ? (
                          <div className="text-sm">
                            <b className="text-ink2">{s.speakerCode}</b>
                            <span className="text-amberdeep font-semibold">
                              {" "}
                              「{s.dialogue}」
                            </span>
                            <span
                              className={`text-[11px] ml-1.5 ${s.dialogue.length > 20 ? "text-red" : "text-ink3"}`}
                            >
                              {s.dialogue.length}字
                            </span>
                          </div>
                        ) : (
                          <span className="text-xs text-ink3">无台词</span>
                        )}
                      </div>
                      <p className="text-xs text-ink2 mt-2 leading-relaxed">
                        {s.visualPrompt}
                      </p>
                    </div>
                  ))}
                </div>
              </>
            )}
          </section>
        )}
        {/* ═══ 定妆照 ═══ */}
        {(!!p.keyvisualUrl ||
          [
            "KEYVISUAL_GENERATING",
            "KEYVISUAL_REVIEW",
            "SHOTS_GENERATING",
            "SHOTS_REVIEW",
            "ASSEMBLING",
            "COMPLETED",
          ].includes(p.status)) && (
          <section>
            <h3 className="font-bold mb-3">② 定妆照</h3>
            <div className="card">
              {!p.keyvisualUrl ? (
                <div className="note note-info">定妆照生成中，约 1-2 分钟…</div>
              ) : (
                <>
                  <img
                    loading="lazy"
                    src={p.keyvisualUrl}
                    alt="定妆照"
                    className="w-full rounded border border-line mb-3"
                  />
                </>
              )}
            </div>
          </section>
        )}
        {/* ═══ 时间线 ═══ */}
        {(p.shots.some((s) => s.videoUrl || s.status !== "PENDING") ||
          [
            "SHOTS_GENERATING",
            "SHOTS_REVIEW",
            "ASSEMBLING",
            "COMPLETED",
          ].includes(p.status)) && (
          <section>
            <div className="flex items-center justify-between gap-3 mb-3 flex-wrap">
              <h3 className="font-bold">
                ③ 镜头进度（{approved}/{p.shots.length} 已完成）
              </h3>
            </div>

            <ShotList shots={p.shots} />
          </section>
        )}{" "}
      </details>
    </div>
  );
}

function Field({ label, v }: { label: string; v: string }) {
  return (
    <div>
      <div className="text-[11px] font-semibold text-ink3 mb-0.5">{label}</div>
      <div className="text-ink2 leading-relaxed">
        {v || <span className="text-ink3">—</span>}
      </div>
    </div>
  );
}

import type { Shot } from "../../lib/api";
import { SHOT_STATUS } from "../../lib/format";
export function ShotList({ shots }: { shots: Shot[] }) {
  return (
    <div className="grid gap-3">
      {shots.map((s) => {
        const st = SHOT_STATUS[s.status] || { label: s.status, cls: "" };
        return (
          <div key={s.id} className="card p-0 overflow-hidden">
            <div className="bg-[#0F1626] flex items-center justify-center min-h-[180px]">
              {s.videoUrl ? (
                <video
                  key={s.videoUrl}
                  controls
                  preload="none"
                  className="w-full max-h-[420px] bg-black"
                  poster={s.frames?.[0] || undefined}
                >
                  <source src={s.videoUrl} type="video/mp4" />
                </video>
              ) : (
                <div className="py-12 text-ink3 text-sm">
                  {s.status === "QUEUED" ? (
                    <>
                      <span className="spin" /> 已入队，生成中（约 7 分钟/段）
                    </>
                  ) : (
                    "尚未生成"
                  )}
                </div>
              )}
            </div>
            <div className="p-4">
              <div className="flex items-center justify-between gap-3 flex-wrap mb-2">
                <div className="font-bold text-sm">
                  {s.shotCode}
                  <span className={`tag ${st.cls} ml-2`}>{st.label}</span>
                </div>
                <div className="text-[11px] text-ink3">
                  {s.shotSize} · {s.cameraMove}
                </div>
              </div>
              {s.dialogue && (
                <div className="bg-ambersoft border-l-[3px] border-amber px-3 py-2 rounded-r text-amberdeep font-semibold text-sm my-2">
                  {s.speakerCode}：「{s.dialogue}」
                </div>
              )}
              <div className="flex gap-4 flex-wrap text-[11px] text-ink3">
                {s.durationActual > 0 && (
                  <span>
                    {s.durationActual.toFixed(2)}s · {s.width}×{s.height}
                  </span>
                )}
                {s.chargeYuan > 0 && (
                  <span>
                    计费 <b className="text-ink2">¥{s.chargeYuan.toFixed(2)}</b>
                  </span>
                )}
                {s.retryCount > 0 && (
                  <span>
                    重试 <b className="text-amberdeep">{s.retryCount}</b>/
                    {s.retryLimit}
                  </span>
                )}
              </div>
              {(s.frames?.length ?? 0) > 0 && (
                <div className="flex gap-1.5 mt-2.5 overflow-x-auto">
                  {(s.frames ?? []).map((f, i) => (
                    <img
                      loading="lazy"
                      key={i}
                      src={f}
                      alt=""
                      className="w-[150px] shrink-0 rounded border border-line cursor-zoom-in"
                      onClick={() => window.open(f, "_blank")}
                    />
                  ))}
                </div>
              )}
              {s.errorMsg && (
                <div className="note note-err my-2 text-xs">{s.errorMsg}</div>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}

import type { CreationDraft } from "./types";
import type { Estimate } from "../../lib/api";
import { DURATION_OPTIONS } from "../../lib/format";
import { Field } from "../../components/ui/Field";
export function Composer({
  draft,
  onChange,
  onSubmit,
  estimate,
  busy,
  error,
  retryEstimate,
}: {
  draft: CreationDraft;
  onChange: (d: CreationDraft) => void;
  onSubmit: () => void;
  estimate: Estimate | null;
  busy: boolean;
  error: string;
  retryEstimate?: () => void;
}) {
  const change = (v: Partial<CreationDraft>) => onChange({ ...draft, ...v });
  return (
    <form
      className="dlg-body"
      onSubmit={(e) => {
        e.preventDefault();
        if (estimate && !busy && draft.idea.trim().length >= 5) onSubmit();
      }}
    >
      <Field
        id="create-idea"
        label="创作想法（一句话即可）"
        hint="至少 5 个字，写清人物、场景或故事冲突。"
      >
        <textarea
          className="input min-h-[96px]"
          maxLength={500}
          value={draft.idea}
          onChange={(e) => change({ idea: e.target.value })}
          placeholder="例：深夜便利店里，店员遇到一位带着旧照片的老人……"
          disabled={busy}
        />
      </Field>
      <div className="grid md:grid-cols-2 gap-4">
        <Field id="create-aspect" label="画面比例">
          <select
            className="input"
            value={draft.aspect}
            onChange={(e) =>
              change({ aspect: e.target.value as CreationDraft["aspect"] })
            }
            disabled={busy}
          >
            <option value="16:9">横屏 16:9</option>
            <option value="9:16">竖屏 9:16</option>
          </select>
        </Field>
        <Field id="create-seconds" label="时长">
          <select
            className="input"
            value={draft.totalSeconds}
            onChange={(e) => change({ totalSeconds: Number(e.target.value) })}
            disabled={busy}
          >
            {DURATION_OPTIONS.map((o) => (
              <option key={o.v} value={o.v}>
                {o.label} · {o.shots} 段
              </option>
            ))}
          </select>
        </Field>
      </div>
      <details>
        <summary>风格与情绪</summary>
        <div className="grid md:grid-cols-2 gap-4 mt-3">
          <Field id="create-film" label="视觉参考">
            <input
              className="input"
              value={draft.filmReference}
              maxLength={128}
              onChange={(e) => change({ filmReference: e.target.value })}
              disabled={busy}
            />
          </Field>
          <Field id="create-mood" label="情绪基调">
            <input
              className="input"
              value={draft.mood}
              maxLength={128}
              onChange={(e) => change({ mood: e.target.value })}
              disabled={busy}
            />
          </Field>
        </div>
      </details>
      <div className="note note-info" aria-live="polite">
        {estimate ? (
          <>
            预计 {estimate.estimatedCredits} 积分 · {estimate.resolution} ·{" "}
            {estimate.segmentCount}{" "}
            段。积分足够直接生成，不足时充值后自动继续。实际消耗按已发生的费用结算。
          </>
        ) : (
          <>
            正在获取服务端估算…
            {retryEstimate && (
              <button type="button" className="btn" onClick={retryEstimate}>
                重试估算
              </button>
            )}
          </>
        )}
      </div>
      {error && (
        <div className="note note-err" role="alert">
          {error}
        </div>
      )}
      <button
        className="btn btn-primary"
        disabled={busy || !estimate || draft.idea.trim().length < 5}
      >
        {busy ? "正在提交…" : "开始生成"}
      </button>
    </form>
  );
}

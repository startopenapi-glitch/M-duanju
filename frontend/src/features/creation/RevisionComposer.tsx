export function RevisionComposer({
  canRevise,
  revisionInstruction,
  setRevisionInstruction,
  busy,
  startRevision,
}: {
  canRevise: boolean;
  revisionInstruction: string;
  setRevisionInstruction: (s: string) => void;
  busy: string;
  startRevision: () => void;
}) {
  return (
    <>
      {" "}
      {canRevise && (
        <section className="card space-y-3" aria-label="制作修改版">
          <label htmlFor="revision-instruction" className="block font-semibold">
            修改要求
          </label>
          <p id="revision-help" className="text-sm text-ink3">
            描述想调整的画面、台词或镜头运动，例如“结尾改成两个人相视微笑”。保留现有角色、场景、时长和画幅，仅重做发生变化的镜头。
          </p>
          <textarea
            id="revision-instruction"
            aria-describedby="revision-help"
            className="input w-full min-h-24"
            rows={3}
            maxLength={2000}
            value={revisionInstruction}
            onChange={(e) => setRevisionInstruction(e.target.value)}
            placeholder="告诉我们，这次想改哪里…"
            disabled={!!busy}
          />
          <p className="text-xs text-ink3">
            积分充足即开始制作；不足时充值到账后自动继续。原版本保留。
          </p>
          <button
            className="btn btn-primary"
            disabled={!!busy || !revisionInstruction.trim()}
            onClick={startRevision}
          >
            {busy === "revise-run" ? "正在准备修改版…" : "生成修改版"}
          </button>
        </section>
      )}
    </>
  );
}

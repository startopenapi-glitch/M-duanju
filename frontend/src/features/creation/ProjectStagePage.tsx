import { useState } from "react";
import { Link, Navigate, useNavigate, useParams } from "react-router-dom";
import { api, errorMessage, type Character, type Project, type Scene, type Shot } from "../../lib/api";
import { toast } from "../../lib/format";
import { OutputPreview } from "./OutputPreview";
import { ShotList } from "./ShotList";
import { ProjectStageNav } from "./ProjectStageNav";
import { useProjectRun } from "./useProjectRun";
import {
  currentStagePath,
  isStageKey,
  stageForProject,
  stageLabel,
  stagePath,
  stageState,
  type StageKey,
} from "./stages";
import { STATUS_LABEL, tagCls } from "../../lib/format";

type Action = (
  key: string,
  fn: () => Promise<unknown>,
  message: string,
  next?: string,
) => Promise<void>;

function ShotEditor({
  shot,
  disabled,
  onSave,
}: {
  shot: Shot;
  disabled: boolean;
  onSave: (shot: Shot, values: Record<string, string>) => void;
}) {
  const [values, setValues] = useState({
    visualPrompt: shot.visualPrompt || "",
    dialogue: shot.dialogue || "",
    cameraMove: shot.cameraMove || "",
    soundscape: shot.soundscape || "",
    musicCue: shot.musicCue || "",
  });
  const set = (key: keyof typeof values, value: string) =>
    setValues((v) => ({ ...v, [key]: value }));
  return (
    <details className="border-t border-line pt-3 mt-3">
      <summary className="cursor-pointer text-sm font-semibold">
        编辑 {shot.shotCode}
      </summary>
      <div className="grid gap-2 mt-3">
        <label className="text-xs text-ink2">
          画面提示
          <textarea
            className="input mt-1 min-h-[80px]"
            value={values.visualPrompt}
            onChange={(e) => set("visualPrompt", e.target.value)}
          />
        </label>
        <label className="text-xs text-ink2">
          台词
          <input
            className="input mt-1"
            value={values.dialogue}
            onChange={(e) => set("dialogue", e.target.value)}
          />
        </label>
        <div className="grid gap-2 md:grid-cols-3">
          <label className="text-xs text-ink2">
            运镜
            <input
              className="input mt-1"
              value={values.cameraMove}
              onChange={(e) => set("cameraMove", e.target.value)}
            />
          </label>
          <label className="text-xs text-ink2">
            环境声
            <input
              className="input mt-1"
              value={values.soundscape}
              onChange={(e) => set("soundscape", e.target.value)}
            />
          </label>
          <label className="text-xs text-ink2">
            音乐提示
            <input
              className="input mt-1"
              value={values.musicCue}
              onChange={(e) => set("musicCue", e.target.value)}
            />
          </label>
        </div>
        <button
          className="btn btn-sm justify-self-start"
          disabled={disabled}
          onClick={() => onSave(shot, values)}
        >
          保存本镜头
        </button>
      </div>
    </details>
  );
}

function CharacterEditor({
  character,
  disabled,
  onSave,
}: {
  character: Character;
  disabled: boolean;
  onSave: (character: Character, values: Record<string, string>) => void;
}) {
  const [values, setValues] = useState({
    name: character.name || "",
    ageRange: character.ageRange || "",
    appearance: character.appearance || "",
    wardrobe: character.wardrobe || "",
    persona: character.persona || "",
    voiceProfile: character.voiceProfile || "",
  });
  const set = (key: keyof typeof values, value: string) =>
    setValues((v) => ({ ...v, [key]: value }));
  return (
    <details className="border-t border-line pt-2 mt-2">
      <summary className="cursor-pointer text-xs font-semibold">
        编辑角色 {character.code}
      </summary>
      <div className="grid gap-2 mt-2">
        <div className="grid gap-2 md:grid-cols-2">
          <label className="text-xs text-ink2">
            姓名
            <input className="input mt-1" value={values.name} onChange={(e) => set("name", e.target.value)} />
          </label>
          <label className="text-xs text-ink2">
            年龄
            <input className="input mt-1" value={values.ageRange} onChange={(e) => set("ageRange", e.target.value)} />
          </label>
        </div>
        <label className="text-xs text-ink2">
          外形
          <textarea className="input mt-1 min-h-[60px]" value={values.appearance} onChange={(e) => set("appearance", e.target.value)} />
        </label>
        <label className="text-xs text-ink2">
          服装
          <input className="input mt-1" value={values.wardrobe} onChange={(e) => set("wardrobe", e.target.value)} />
        </label>
        <label className="text-xs text-ink2">
          性格与说话方式
          <input className="input mt-1" value={values.persona} onChange={(e) => set("persona", e.target.value)} />
        </label>
        <label className="text-xs text-ink2">
          声线
          <input className="input mt-1" value={values.voiceProfile} onChange={(e) => set("voiceProfile", e.target.value)} />
        </label>
        <button className="btn btn-sm justify-self-start" disabled={disabled} onClick={() => onSave(character, values)}>
          保存角色
        </button>
      </div>
    </details>
  );
}

function SceneEditor({
  scene,
  disabled,
  onSave,
}: {
  scene: Scene;
  disabled: boolean;
  onSave: (scene: Scene, values: Record<string, string>) => void;
}) {
  const [values, setValues] = useState({
    name: scene.name || "",
    location: scene.location || "",
    timeOfDay: scene.timeOfDay || "",
    lighting: scene.lighting || "",
    colorTemp: scene.colorTemp || "",
    props: (scene.props ?? []).join("、"),
  });
  const set = (key: keyof typeof values, value: string) =>
    setValues((v) => ({ ...v, [key]: value }));
  return (
    <details className="border-t border-line pt-2 mt-2">
      <summary className="cursor-pointer text-xs font-semibold">
        编辑场景 {scene.code}
      </summary>
      <div className="grid gap-2 mt-2">
        <div className="grid gap-2 md:grid-cols-2">
          <label className="text-xs text-ink2">
            名称
            <input className="input mt-1" value={values.name} onChange={(e) => set("name", e.target.value)} />
          </label>
          <label className="text-xs text-ink2">
            地点
            <input className="input mt-1" value={values.location} onChange={(e) => set("location", e.target.value)} />
          </label>
        </div>
        <div className="grid gap-2 md:grid-cols-3">
          <label className="text-xs text-ink2">
            时间
            <input className="input mt-1" value={values.timeOfDay} onChange={(e) => set("timeOfDay", e.target.value)} />
          </label>
          <label className="text-xs text-ink2">
            光位
            <input className="input mt-1" value={values.lighting} onChange={(e) => set("lighting", e.target.value)} />
          </label>
          <label className="text-xs text-ink2">
            色温
            <input className="input mt-1" value={values.colorTemp} onChange={(e) => set("colorTemp", e.target.value)} />
          </label>
        </div>
        <label className="text-xs text-ink2">
          道具（用、分隔）
          <input className="input mt-1" value={values.props} onChange={(e) => set("props", e.target.value)} />
        </label>
        <button className="btn btn-sm justify-self-start" disabled={disabled} onClick={() => onSave(scene, values)}>
          保存场景
        </button>
      </div>
    </details>
  );
}

function ScriptView({
  project,
  busy,
  onGenerate,
  onConfirm,
  onSaveShot,
  onSaveCharacter,
  onSaveScene,
}: {
  project: Project;
  busy: string;
  onGenerate: () => void;
  onConfirm: () => void;
  onSaveShot: (shot: Shot, values: Record<string, string>) => void;
  onSaveCharacter: (character: Character, values: Record<string, string>) => void;
  onSaveScene: (scene: Scene, values: Record<string, string>) => void;
}) {
  const hasScript = (project.shots ?? []).length > 0;
  return (
    <div className="grid gap-4">
      <section className="card">
        <div className="flex items-start justify-between gap-3 flex-wrap">
          <div>
            <h2 className="text-lg font-bold">剧本</h2>
            <p className="text-sm text-ink2 mt-2">{project.idea}</p>
          </div>
          <div className="flex gap-2">
            {project.status === "DRAFT" || !hasScript ? (
              <button
                className="btn btn-primary btn-sm"
                disabled={!!busy}
                onClick={onGenerate}
              >
                {busy === "script-generate" ? "生成中…" : "生成剧本"}
              </button>
            ) : (
              <>
                <button
                  className="btn btn-sm"
                  disabled={!!busy}
                  onClick={onGenerate}
                >
                  重新生成
                </button>
                {project.status === "SCRIPT_REVIEW" && (
                  <button
                    className="btn btn-primary btn-sm"
                    disabled={!!busy}
                    onClick={onConfirm}
                  >
                    确认剧本并生成定妆照（30 积分）
                  </button>
                )}
              </>
            )}
          </div>
        </div>
        <div className="grid gap-3 mt-4 md:grid-cols-2">
          <div>
            <b className="text-sm">角色</b>
            {(project.characters ?? []).length === 0 ? (
              <p className="text-sm text-ink3">暂无角色</p>
            ) : (
              <ul className="text-sm mt-1 space-y-1">
                {(project.characters ?? []).map((c) => (
                  <li key={c.id}>
                    <div>
                      {c.code} · {c.name}（{c.ageRange || "年龄未设置"}）
                    </div>
                    <CharacterEditor
                      character={c}
                      disabled={!!busy || project.status !== "SCRIPT_REVIEW"}
                      onSave={onSaveCharacter}
                    />
                  </li>
                ))}
              </ul>
            )}
          </div>
          <div>
            <b className="text-sm">场景</b>
            {(project.scenes ?? []).length === 0 ? (
              <p className="text-sm text-ink3">暂无场景</p>
            ) : (
              <ul className="text-sm mt-1 space-y-1">
                {(project.scenes ?? []).map((s) => (
                  <li key={s.id}>
                    <div>
                      {s.code} · {s.name}
                    </div>
                    <SceneEditor
                      scene={s}
                      disabled={!!busy || project.status !== "SCRIPT_REVIEW"}
                      onSave={onSaveScene}
                    />
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      </section>
      <section className="card">
        <h2 className="text-lg font-bold">分镜</h2>
        {(project.shots ?? []).length === 0 ? (
          <p className="text-sm text-ink3 mt-2">暂无分镜，请先生成剧本。</p>
        ) : (
          <ol className="mt-3 space-y-2 text-sm">
            {(project.shots ?? []).map((s) => (
              <li key={s.id} className="border-b border-line pb-3 last:border-0">
                <b>{s.shotCode}</b> · {s.duration}s · {s.cameraMove || "固定"}
                <p className="text-ink2">{s.visualPrompt}</p>
                <ShotEditor
                  shot={s}
                  disabled={!!busy || project.status !== "SCRIPT_REVIEW"}
                  onSave={onSaveShot}
                />
              </li>
            ))}
          </ol>
        )}
      </section>
      <div className="note note-info">
        确认剧本之前不会生成定妆照；确认后只派发定妆照，不会自动进入镜头。
      </div>
    </div>
  );
}

function KeyVisualView({
  project,
  busy,
  onRegenerate,
  onConfirm,
}: {
  project: Project;
  busy: string;
  onRegenerate: () => void;
  onConfirm: () => void;
}) {
  return (
    <section className="card space-y-3">
      <div className="flex items-start justify-between gap-3 flex-wrap">
        <h2 className="text-lg font-bold">定妆照</h2>
        {project.status === "KEYVISUAL_REVIEW" && (
          <div className="flex gap-2">
            <button className="btn btn-sm" disabled={!!busy} onClick={onRegenerate}>
              重做定妆照
            </button>
            <button
              className="btn btn-primary btn-sm"
              disabled={!!busy}
              onClick={onConfirm}
            >
              确认定妆照并生成镜头
            </button>
          </div>
        )}
      </div>
      {project.keyvisualUrl ? (
        <img
          src={project.keyvisualUrl}
          alt="定妆照"
          className="max-h-[520px] rounded border border-line object-contain"
        />
      ) : (
        <p className="text-sm text-ink2">
          {project.status === "KEYVISUAL_GENERATING"
            ? "定妆照生成中，请稍候…"
            : "尚未生成定妆照。请先确认剧本。"}
        </p>
      )}
      <div className="note note-info">
        确认定妆照后才会逐镜头派发；镜头按现有单段价格预占。
      </div>
    </section>
  );
}

function ShotsView({
  project,
  busy,
  onApprove,
  onUnapprove,
  onRegenerate,
  onAssemble,
}: {
  project: Project;
  busy: string;
  onApprove: (seq: number) => void;
  onUnapprove: (seq: number) => void;
  onRegenerate: (seq: number) => void;
  onAssemble: () => void;
}) {
  const shots = project.shots ?? [];
  const allApproved =
    shots.length > 0 &&
    shots.every((s) => s.status === "APPROVED" && !!s.videoUrl);
  return (
    <section className="space-y-3">
      <div className="card">
        <div className="flex items-start justify-between gap-3 flex-wrap">
          <div>
            <h2 className="text-lg font-bold">镜头</h2>
            <p className="text-sm text-ink2 mt-1">
              已生成 {shots.filter((s) => !!s.videoUrl).length} / {shots.length} 段；
              全部通过后才能合成。
            </p>
          </div>
          <button
            className="btn btn-primary btn-sm"
            disabled={!!busy || !allApproved}
            onClick={onAssemble}
          >
            {busy === "assemble" ? "合成中…" : "合成成片"}
          </button>
        </div>
      </div>
      <ShotList
        shots={shots}
        actions={{
          busy: !!busy,
          onApprove,
          onUnapprove,
          onRegenerate,
        }}
      />
    </section>
  );
}

export default function ProjectStagePage() {
  const { id = "", stage } = useParams();
  const navigate = useNavigate();
  const { project, runView, readState, error, refresh } = useProjectRun(id);
  const [busy, setBusy] = useState("");
  const requested: StageKey | null = isStageKey(stage) ? stage : null;

  const runAction: Action = async (key, fn, message, next) => {
    setBusy(key);
    try {
      await fn();
      await refresh();
      toast(message, "ok");
      if (next) navigate(next);
    } catch (e) {
      toast(errorMessage(e), "err");
    } finally {
      setBusy("");
    }
  };

  if (!project || project.id !== id) {
    return (
      <div className="card" role={error ? "alert" : "status"}>
        {error ? (
          <>
            项目加载失败：{error}
            <button className="btn" onClick={() => void refresh()}>
              重试
            </button>
          </>
        ) : (
          "载入中…"
        )}
      </div>
    );
  }

  const current = stageForProject(project);
  if (!requested) return <Navigate to={currentStagePath(project)} replace />;
  if (stageState(project, requested) === "locked")
    return <Navigate to={stagePath(project.id, current)} replace />;

  const generateScript = () =>
    runAction(
      "script-generate",
      () => api.post(`/projects/${id}/script/generate`, {}),
      "剧本已生成，请检查后确认",
    );
  const confirmScript = () =>
    runAction(
      "script-confirm",
      () => api.post(`/projects/${id}/script/confirm`, {}),
      "剧本已确认，开始生成定妆照",
      stagePath(id, "keyvisual"),
    );
  const regenerateKeyVisual = () => {
    if (!window.confirm("重新生成定妆照会再次预占 30 积分，确定继续？")) return;
    void runAction(
      "keyvisual-generate",
      () => api.post(`/projects/${id}/keyvisual/queue`, {}),
      "已重新提交定妆照",
    );
  };
  const confirmKeyVisual = () =>
    runAction(
      "keyvisual-confirm",
      () => api.post(`/projects/${id}/keyvisual/confirm`, {}),
      "定妆照已确认，开始生成镜头",
      stagePath(id, "shots"),
    );
  const saveShot = (shot: Shot, values: Record<string, string>) =>
    runAction(
      "save-shot-" + shot.seq,
      () =>
        api.patch(`/projects/${id}/shots/${shot.id}`, {
          visualPrompt: values.visualPrompt,
          dialogue: values.dialogue,
          cameraMove: values.cameraMove,
          soundscape: values.soundscape,
          musicCue: values.musicCue,
        }),
      "镜头 " + shot.shotCode + " 已保存",
    );
  const saveCharacter = (
    character: Character,
    values: Record<string, string>,
  ) =>
    runAction(
      "save-character-" + character.code,
      () => api.patch(`/projects/${id}/characters/${character.id}`, values),
      "角色 " + character.name + " 已保存",
    );
  const saveScene = (scene: Scene, values: Record<string, string>) =>
    runAction(
      "save-scene-" + scene.code,
      () =>
        api.patch(`/projects/${id}/scenes/${scene.id}`, {
          name: values.name,
          location: values.location,
          timeOfDay: values.timeOfDay,
          lighting: values.lighting,
          colorTemp: values.colorTemp,
          props: values.props
            .split(/[、,，]/)
            .map((x) => x.trim())
            .filter(Boolean),
        }),
      "场景 " + scene.name + " 已保存",
    );
  const approveShot = (seq: number) =>
    runAction(
      "approve-" + seq,
      () => api.post(`/projects/${id}/shots/${seq}/approve`, {}),
      "镜头已通过",
    );
  const unapproveShot = (seq: number) =>
    runAction(
      "unapprove-" + seq,
      () => api.post(`/projects/${id}/shots/${seq}/unapprove`, {}),
      "已取消通过",
    );
  const regenerateShot = (seq: number) => {
    if (!window.confirm("重做镜头会再次预占该段费用，确定继续？")) return;
    void runAction(
      "regenerate-" + seq,
      () =>
        api.post(`/projects/${id}/shots/${seq}/regenerate`, {
          overrideLimit: true,
        }),
      "已重新提交镜头",
    );
  };
  const assemble = () =>
    runAction(
      "assemble",
      () => api.post(`/projects/${id}/assemble`, {}),
      "已提交合成",
      stagePath(id, "final"),
    );

  return (
    <div className="space-y-5">
      <div className="flex items-start justify-between gap-4 flex-wrap">
        <div>
          <Link to="/" className="text-sm text-ink3">
            ← 我的项目
          </Link>
          <h1 className="text-2xl font-bold mt-1">{project.title}</h1>
          <p className="text-sm text-ink3 mt-1 max-w-2xl">{project.idea}</p>
        </div>
        <div className="text-right">
          <span className={`tag ${tagCls(project.status)}`}>
            {STATUS_LABEL[project.status] || project.status}
          </span>
          <div className="text-xs text-ink3 mt-2">
            {project.workflowMode === "manual" ? "手动制作" : "历史执行"}
          </div>
        </div>
      </div>
      <ProjectStageNav project={project} />
      <div className="note note-info" role="status">
        {project.workflowMode === "manual"
          ? `当前：${stageLabel(current)}阶段。上一环节确认后才会执行下一环节。`
          : "这是停止自动推进后的历史项目，已提交任务仍会核对和结算。"}
      </div>
      {requested === "script" && (
        <ScriptView
          project={project}
          busy={busy}
          onGenerate={generateScript}
          onConfirm={confirmScript}
          onSaveShot={saveShot}
          onSaveCharacter={saveCharacter}
          onSaveScene={saveScene}
        />
      )}
      {requested === "keyvisual" && (
        <KeyVisualView
          project={project}
          busy={busy}
          onRegenerate={regenerateKeyVisual}
          onConfirm={confirmKeyVisual}
        />
      )}
      {requested === "shots" && (
        <ShotsView
          project={project}
          busy={busy}
          onApprove={approveShot}
          onUnapprove={unapproveShot}
          onRegenerate={regenerateShot}
          onAssemble={assemble}
        />
      )}
      {requested === "final" && (
        <OutputPreview
          project={project}
          runView={runView}
          readState={readState}
        />
      )}
    </div>
  );
}

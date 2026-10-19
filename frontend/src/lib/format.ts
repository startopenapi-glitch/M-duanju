export const STATUS_LABEL: Record<string,string> = {
  DRAFT:'草稿', SCRIPT_GENERATING:'剧本生成中', SCRIPT_REVIEW:'剧本待确认',
  KEYVISUAL_GENERATING:'定妆照生成中', KEYVISUAL_REVIEW:'定妆照待确认',
  SHOTS_GENERATING:'分镜生成中', SHOTS_REVIEW:'分镜待审片',
  ASSEMBLING:'合成中', COMPLETED:'已完成', FAILED:'失败', BUDGET_EXCEEDED:'预算熔断'
}
export const SHOT_STATUS: Record<string,{label:string;cls:string}> = {
  PENDING:{label:'待生成',cls:''}, QUEUED:{label:'已入队',cls:'tag-navy'},
  GENERATED:{label:'待审片',cls:'tag-amber'}, APPROVED:{label:'已通过',cls:'tag-green'},
  FAILED:{label:'失败',cls:'tag-red'}, MANUAL_REQUIRED:{label:'需人工处理',cls:'tag-red'}
}
export function tagCls(status: string) {
  if (status === 'COMPLETED') return 'tag-green'
  if (status === 'FAILED' || status === 'BUDGET_EXCEEDED') return 'tag-red'
  if (['SCRIPT_REVIEW','KEYVISUAL_REVIEW','SHOTS_REVIEW'].includes(status)) return 'tag-amber'
  if (status.endsWith('_GENERATING')) return 'tag-navy'
  return ''
}
/** 积分→元 */
export const creditsToYuan = (c: number) => (c / 100).toFixed(2)
/** 时长选项（每段 5 秒固定） */
export const DURATION_OPTIONS = [
  { v: 5,   label: '5 秒',   shots: 1,  time: '约 8 分钟' },
  { v: 15,  label: '15 秒',  shots: 3,  time: '约 12 分钟' },
  { v: 30,  label: '30 秒',  shots: 6, time: '约 20 分钟' },
  { v: 60,  label: '1 分钟', shots: 12, time: '约 35 分钟' },
  { v: 120, label: '2 分钟', shots: 24, time: '约 60 分钟' },
  { v: 180, label: '3 分钟', shots: 36, time: '约 90 分钟' },
]
export function toast(msg: string, type: 'info'|'err'|'ok' = 'info') {
  const el = document.createElement('div')
  el.className = `note note-${type === 'info' ? 'info' : type === 'err' ? 'err' : 'ok'}`
  el.style.cssText = 'position:fixed;top:18px;right:18px;z-index:200;max-width:min(420px,calc(100vw - 36px));overflow-wrap:anywhere;box-shadow:0 4px 20px rgba(0,0,0,.14)'
  el.textContent = msg
  document.body.appendChild(el)
  setTimeout(() => el.remove(), type === 'err' ? 9000 : 4000)
}

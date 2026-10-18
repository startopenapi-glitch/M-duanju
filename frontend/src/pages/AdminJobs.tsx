import {Status} from '../features/admin/presentation'
import { useState } from 'react'
import { api, type GenerationRun } from '../lib/api'
import { useResource, useAction } from '../features/workspace/resource'
import {
  PageHeader,
  State,
  Pager,
  Filters,
  Detail,
  Facts,
  Timeline,
  Feedback,
  Table,
  Cell,
  day,
  type Page,
  type Audit,
} from '../features/workspace/ui'
import type { Job } from '../features/admin/types'
export default function AdminJobs() {
  const [kw, setKw] = useState(''),
    [status, setStatus] = useState(''),
    [page, setPage] = useState(1),
    [selected, setSelected] = useState('')
  const q = useResource<Page<Job>>(
    `/admin/jobs/list?kw=${encodeURIComponent(kw)}&status=${encodeURIComponent(status)}&page=${page}`,
  )
  return (
    <div className="ws-page">
      <PageHeader
        title="创作任务"
        description="核对执行进度、上游回执与预占积分，安全处理未知提交。"
      />
      <section className="ws-section">
        <Filters
          keyword={kw}
          onKeyword={(v) => {
            setKw(v)
            setPage(1)
          }}
          status={status}
          onStatus={(v) => {
            setStatus(v)
            setPage(1)
          }}
          label="任务状态"
          statuses={[
            ['queued', '排队中'],
            ['running', '执行中'],
            ['reconcile', '待核对'],
            ['failed', '失败'],
            ['done', '已完成'],
            ['cancelled', '已取消'],
            ['abandoned', '已终止'],
          ]}
        />
        <State
          {...q}
          empty={q.data?.items.length === 0}
          emptyLabel="暂无创作任务"
        />
        {q.data && !!q.data.items.length && (
          <Table
            head={[
              '任务 / 项目',
              '用户',
              '状态 / 阶段',
              '预占 / 尝试',
              '更新时间',
              '操作',
            ]}
          >
            {q.data.items.map((j) => (
              <tr key={j.id}>
                <Cell label="任务 / 项目">
                  <b>{j.projectTitle || j.projectId}</b>
                  <small className="ws-id">{j.id}</small>
                  <small>{j.kind}</small>
                </Cell>
                <Cell label="用户">{j.userEmail || j.userId}</Cell>
                <Cell label="状态 / 阶段">
                  <Status value={j.status} />
                  <small>{j.phase}</small>
                </Cell>
                <Cell label="预占 / 尝试">
                  {j.reservedCredits} 积分
                  <small>
                    {j.attempts} / {j.maxAttempts} 次
                  </small>
                </Cell>
                <Cell label="更新时间">{day(j.updatedAt)}</Cell>
                <Cell label="操作">
                  <button
                    className="btn btn-sm"
                    aria-label={`查看任务 ${j.id}`}
                    onClick={() => setSelected(j.id)}
                  >
                    查看详情
                  </button>
                </Cell>
              </tr>
            ))}
          </Table>
        )}
        {q.data && (
          <Pager
            page={page}
            total={q.data.total}
            pageSize={q.data.pageSize}
            onPage={setPage}
          />
        )}
      </section>
      {selected && (
        <JobPanel
          id={selected}
          onClose={() => setSelected('')}
          changed={q.retry}
        />
      )}
    </div>
  )
}
function JobPanel({
  id,
  onClose,
  changed,
}: {
  id: string
  onClose: () => void
  changed: () => void
}) {
  const q = useResource<{
      job: Job
      run: GenerationRun | null
      events: Audit[]
      shots?: {
        id: string
        seq: number
        status: string
        upstreamId: string
        updatedAt: string
      }[]
      receipts?: {
        id: string
        upstreamId: string
        kind: string
        status: string
        chargeMicros: number
        credits: number
        createdAt: string
        settledAt: string | null
      }[]
    }>(`/admin/jobs/${id}/detail`),
    a = useAction(),
    d = q.data
  const [taskId, setTaskId] = useState(''),
    [reason, setReason] = useState(''),
    [verified, setVerified] = useState(false)
  const unknown =
    d?.job.errorCode === 'submission_unknown' && d.job.status === 'reconcile'
  return (
    <Detail title={`任务 ${id}`} onClose={onClose}>
      <State {...q} />
      {d && (
        <>
          <Facts
            items={[
              ['任务 ID', d.job.id],
              ['项目', d.job.projectTitle || d.job.projectId],
              ['用户', d.job.userEmail || d.job.userId],
              ['任务类型', d.job.kind],
              ['任务状态', <Status value={d.job.status} />],
              ['执行阶段', d.job.phase],
              ['上游任务 / 回执', d.job.upstreamId || '尚无已核实回执'],
              ['预占积分', d.job.reservedCredits],
              ['执行 ID', d.run?.id || d.job.runId],
              [
                '执行状态',
                d.run ? <Status value={d.run.status} /> : '无关联执行',
              ],
              ['执行检查点', d.run?.checkpoint || '—'],
              ['尝试次数', `${d.job.attempts} / ${d.job.maxAttempts}`],
              ['错误代码', d.job.errorCode || '—'],
              ['错误详情', d.job.lastError || '—'],
            ]}
          />
          <Feedback action={a} />
          {unknown && (
            <section className="ws-section">
              <h3>核对未知提交</h3>
              <p>
                先在供货方核实任务归属及回执。提交只绑定已存在的上游任务，不重发生成，不释放预占。
              </p>
              <label className="ws-field">
                已核实的上游任务 ID
                <input
                  className="input"
                  value={taskId}
                  onChange={(e) => setTaskId(e.target.value)}
                />
              </label>
              <label className="ws-field">
                核对依据
                <input
                  className="input"
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                />
              </label>
              <label className="ws-check">
                <input
                  type="checkbox"
                  checked={verified}
                  onChange={(e) => setVerified(e.target.checked)}
                />
                已在供货方核实该任务属于此项目
              </label>
              <button
                className="btn btn-primary"
                disabled={
                  a.busy || !taskId.trim() || !reason.trim() || !verified
                }
                onClick={() =>
                  a.run(
                    () =>
                      api.post(`/admin/jobs/${id}/reconcile`, {
                        project: d.job.projectId,
                        verifiedTaskId: taskId.trim(),
                        reason: reason.trim(),
                      }),
                    '已绑定核实回执，任务将继续核对结算',
                    () => {
                      q.retry()
                      changed()
                    },
                  )
                }
              >
                {a.busy ? '核对中…' : '提交核对结果'}
              </button>
            </section>
          )}
          <section className="ws-section">
            <h3>镜头与回执</h3>
            {d.shots?.length ? (
              <Table head={['镜头', '状态', '上游任务', '更新时间']}>
                {d.shots.map((s) => (
                  <tr key={s.id}>
                    <Cell label="镜头">
                      第 {s.seq} 镜<small>{s.id}</small>
                    </Cell>
                    <Cell label="状态">
                      <Status value={s.status} />
                    </Cell>
                    <Cell label="上游任务">{s.upstreamId || '—'}</Cell>
                    <Cell label="更新时间">{day(s.updatedAt)}</Cell>
                  </tr>
                ))}
              </Table>
            ) : (
              <p className="ws-muted">此任务暂无关联镜头记录。</p>
            )}
            {d.receipts?.length ? (
              <ol className="ws-timeline">
                {d.receipts.map((r) => (
                  <li key={r.id}>
                    <span>{day(r.createdAt)}</span>
                    <b>
                      {r.kind} · {r.upstreamId}
                    </b>
                    <p>
                      <Status value={r.status} context="receipt" /> ·{' '}
                      {r.credits} 积分
                    </p>
                    <p>结算时间：{r.settledAt ? day(r.settledAt) : '未确认'}</p>
                  </li>
                ))}
              </ol>
            ) : (
              <p className="ws-muted">暂无已记录的上游结算回执。</p>
            )}
          </section>
          <Timeline items={d.events} />
        </>
      )}
    </Detail>
  )
}

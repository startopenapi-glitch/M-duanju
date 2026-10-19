import { useState } from 'react'
import { useResource } from '../features/workspace/resource'
import {
  PageHeader,
  State,
  Pager,
  Filters,
  Facts,
  Table,
  Cell,
  day,
  type Page,
  type Audit,
} from '../features/workspace/ui'
type System = {
  database: string
  worker: { status: string; lastHeartbeat: string | null }
  payments: { enabled: boolean; mock: boolean }
  mailConfigured: boolean
  asOf: string
}
export default function AdminSystem() {
  const [tab, setTab] = useState('system'),
    [kw, setKw] = useState(''),
    [page, setPage] = useState(1)
  const system = useResource<System>(tab === 'system' ? '/admin/system' : null),
    audit = useResource<Page<Audit>>(
      tab === 'audit'
        ? `/admin/audit?kw=${encodeURIComponent(kw)}&page=${page}`
        : null,
    )
  const d = system.data
  return (
    <div className="ws-page">
      <PageHeader
        title="系统与审计"
        description="只读查看依赖状态，按关键词追溯运营操作。"
      />
      <div className="ws-tabs" role="tablist" aria-label="系统与审计">
        <button
          role="tab"
          aria-selected={tab === 'system'}
          onClick={() => setTab('system')}
        >
          系统状态
        </button>
        <button
          role="tab"
          aria-selected={tab === 'audit'}
          onClick={() => setTab('audit')}
        >
          审计日志
        </button>
      </div>
      {tab === 'system' ? (
        <>
          <State {...system} />
          {d && (
            <>
              <section className="ws-section">
                <h2>依赖与配置</h2>
                <Facts
                  items={[
                    [
                      '数据库',
                      d.database === 'ok'
                        ? '连接正常'
                        : `待检查（${d.database}）`,
                    ],
                    [
                      '生成 Worker',
                      d.worker.status === 'healthy'
                        ? '心跳正常'
                        : d.worker.status === 'stale'
                          ? '心跳过期，需排查'
                          : '健康状态未知',
                    ],
                    ['最近 Worker 心跳', day(d.worker.lastHeartbeat || '')],
                    ['支付功能', d.payments.enabled ? '已启用' : '未启用'],
                    ['支付模式', d.payments.mock ? '模拟渠道' : '真实渠道'],
                    ['邮件配置', d.mailConfigured ? '已配置' : '未配置'],
                  ]}
                />
                <p>配置仅显示脱敏状态。未知心跳不能说明 Worker 正常运行。</p>
                <button className="btn" onClick={system.retry}>
                  刷新状态
                </button>
              </section>
              <p className="ws-muted">数据截至 {day(d.asOf)}</p>
            </>
          )}
        </>
      ) : (
        <section className="ws-section">
          <Filters
            keyword={kw}
            onKeyword={(v) => {
              setKw(v)
              setPage(1)
            }}
          />
          <State
            {...audit}
            empty={audit.data?.items.length === 0}
            emptyLabel="暂无审计记录"
          />
          {audit.data && !!audit.data.items.length && (
            <Table head={['操作', '操作人', '项目', '状态变更 / 说明', '时间']}>
              {audit.data.items.map((e) => (
                <tr key={e.id}>
                  <Cell label="操作">
                    <b>{e.action}</b>
                    <small>{e.id}</small>
                  </Cell>
                  <Cell label="操作人">{e.userId || '系统'}</Cell>
                  <Cell label="项目">{e.projectId || '—'}</Cell>
                  <Cell label="状态变更 / 说明">
                    {e.fromStatus || e.toStatus
                      ? `${e.fromStatus || '—'} → ${e.toStatus || '—'}`
                      : '—'}
                    <small>{e.note}</small>
                  </Cell>
                  <Cell label="时间">{day(e.createdAt)}</Cell>
                </tr>
              ))}
            </Table>
          )}
          {audit.data && (
            <Pager
              page={page}
              total={audit.data.total}
              pageSize={audit.data.pageSize}
              onPage={setPage}
            />
          )}
        </section>
      )}
    </div>
  )
}

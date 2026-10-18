import {Status} from '../features/admin/presentation'
import { useRef, useState } from 'react'
import { api, ApiError, type User } from '../lib/api'
import { useAuth } from '../App'
import { useResource, useAction } from '../features/workspace/resource'
import {
  PageHeader,
  State,
  Pager,
  Filters,
  Detail,
  Facts,
  Feedback,
  Table,
  Cell,
  day,
  type Page,
} from '../features/workspace/ui'
import type { UserDetail } from '../features/admin/types'
import { CreditTable } from './AdminOrders'
export default function AdminUsers() {
  const [kw, setKw] = useState(''),
    [page, setPage] = useState(1),
    [selected, setSelected] = useState('')
  const q = useResource<Page<User>>(
    `/admin/users?kw=${encodeURIComponent(kw)}&page=${page}`,
  )
  return (
    <div className="ws-page">
      <PageHeader
        title="用户管理"
        description="核对账户状态、可用积分与预占，调整操作完整留痕。"
      />
      <section className="ws-section">
        <Filters
          keyword={kw}
          onKeyword={(v) => {
            setKw(v)
            setPage(1)
          }}
        />
        <State
          {...q}
          empty={q.data?.items.length === 0}
          emptyLabel="暂无用户"
        />
        {q.data && !!q.data.items.length && (
          <Table
            head={['用户', '角色', '状态', '积分余额', '注册时间', '操作']}
          >
            {q.data.items.map((u) => (
              <tr key={u.id}>
                <Cell label="用户">
                  <b>{u.nickname || u.email}</b>
                  <small>{u.email}</small>
                  <small>{u.id}</small>
                </Cell>
                <Cell label="角色">
                  {u.role === 'admin' ? '管理员' : '用户'}
                </Cell>
                <Cell label="状态">
                  <Status value={u.status} />
                </Cell>
                <Cell label="积分余额">{u.credits}</Cell>
                <Cell label="注册时间">{day(u.createdAt)}</Cell>
                <Cell label="操作">
                  <button
                    className="btn btn-sm"
                    aria-label={`查看用户 ${u.email}`}
                    onClick={() => setSelected(u.id)}
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
        <UserPanel
          key={selected}
          id={selected}
          onClose={() => setSelected('')}
          changed={q.retry}
        />
      )}
    </div>
  )
}
function UserPanel({
  id,
  onClose,
  changed,
}: {
  id: string
  onClose: () => void
  changed: () => void
}) {
  const { user } = useAuth(),
    q = useResource<UserDetail>(`/admin/users/${id}/detail`),
    a = useAction(),
    d = q.data
  type Adjustment = {
    userId: string
    amount: number
    note: string
    requestId: string
  }
  const storageKey = `drama:admin-adjustment:${user?.id}:${id}`
  const [initial] = useState<Adjustment | null>(() => {
    try {
      const saved = JSON.parse(
        sessionStorage.getItem(storageKey) || 'null',
      ) as Adjustment | null
      return saved?.userId === id &&
        Number.isSafeInteger(saved.amount) &&
        saved.amount !== 0 &&
        typeof saved.note === 'string' &&
        typeof saved.requestId === 'string'
        ? saved
        : null
    } catch {
      return null
    }
  })
  const [amount, setAmount] = useState(initial ? String(initial.amount) : ''),
    [note, setNote] = useState(initial?.note || ''),
    [reason, setReason] = useState(''),
    [validation, setValidation] = useState('')
  const pending = useRef<Adjustment | null>(initial)
  const [uncertain, setUncertain] = useState(!!initial)
  const adjust = () => {
    if (!pending.current) {
      const n = Number(amount)
      if (
        !Number.isSafeInteger(n) ||
        !n ||
        !note.trim() ||
        note.trim().length > 255
      ) {
        setValidation('请输入非零整数积分和调整原因')
        return
      }
      const next: Adjustment = {
        userId: id,
        amount: n,
        note: note.trim(),
        requestId: crypto.randomUUID(),
      }
      try {
        sessionStorage.setItem(storageKey, JSON.stringify(next))
      } catch {
        setValidation('无法保存防重复请求标识，请检查浏览器存储后重试。')
        return
      }
      pending.current = next
    }
    setValidation('')
    setUncertain(true)
    a.run(
      () => api.post('/admin/users/credits', pending.current),
      '积分调整成功',
      () => {
        try {
          sessionStorage.removeItem(storageKey)
        } catch {
          /* A retained successful request is safe to replay. */
        }
        pending.current = null
        setUncertain(false)
        setAmount('')
        setNote('')
        q.retry()
        changed()
      },
      (error) => {
        if (error instanceof ApiError && [400, 404].includes(error.status)) {
          try {
            sessionStorage.removeItem(storageKey)
          } catch {
            /* Retaining an invalid request causes no money movement. */
          }
          pending.current = null
          setUncertain(false)
        }
      },
    )
  }
  return (
    <Detail title="用户详情" onClose={onClose}>
      <State {...q} />
      {d && (
        <>
          <Facts
            items={[
              ['邮箱', d.user.email],
              ['用户 ID', d.user.id],
              ['角色', d.user.role === 'admin' ? '管理员' : '用户'],
              ['状态', <Status value={d.user.status} />],
              ['总积分', d.user.credits],
              ['可用积分', d.availableCredits],
              ['预占积分', d.reservedCredits],
              ['项目数', d.projectsCount],
              ['订单数', d.ordersCount],
              ['状态版本', d.version],
            ]}
          />
          <Feedback action={a} />
          <button className="btn" disabled={a.busy} onClick={q.retry}>
            刷新用户详情
          </button>
          <section className="ws-section">
            <h3>调整积分</h3>
            <p>
              使用独立请求标识，网络结果未知时仅重试原请求。预占中的积分不会被移走。
            </p>
            <label className="ws-field">
              调整积分
              <input
                className="input"
                type="number"
                step="1"
                value={amount}
                disabled={uncertain || a.busy}
                onChange={(e) => setAmount(e.target.value)}
              />
            </label>
            <label className="ws-field">
              调整原因
              <input
                className="input"
                value={note}
                maxLength={255}
                disabled={uncertain || a.busy}
                onChange={(e) => setNote(e.target.value)}
              />
            </label>
            {validation && (
              <p role="alert" className="ws-error">
                {validation}
              </p>
            )}
            <button
              className="btn btn-primary"
              disabled={a.busy}
              onClick={adjust}
            >
              {a.busy ? '提交中…' : uncertain ? '重试原调整' : '确认调整'}
            </button>
          </section>
          <section className="ws-section">
            <h3>账户状态</h3>
            <p>
              停用会使已有会话失效并阻止新增生成；已提交任务仍可核对结算。不能停用本人或最后一位有效管理员。
            </p>
            <label className="ws-field">
              状态变更原因
              <input
                className="input"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
            </label>
            <button
              className="btn"
              disabled={
                a.busy ||
                !reason.trim() ||
                (d.user.id === user?.id && d.user.status !== 'banned')
              }
              onClick={() =>
                a.run(
                  () =>
                    api.post(`/admin/users/${id}/status`, {
                      status: d.user.status === 'banned' ? 'active' : 'banned',
                      version: d.version,
                      reason: reason.trim(),
                    }),
                  '账户状态已更新',
                  () => {
                    setReason('')
                    q.retry()
                    changed()
                  },
                )
              }
            >
              {d.user.status === 'banned' ? '恢复账户' : '停用账户'}
            </button>
          </section>
          <section className="ws-section">
            <h3>最近积分流水</h3>
            {d.transactions.length ? (
              <CreditTable items={d.transactions} />
            ) : (
              <p className="ws-muted">暂无积分流水</p>
            )}
          </section>
        </>
      )}
    </Detail>
  )
}

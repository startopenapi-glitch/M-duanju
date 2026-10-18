import {Status} from '../features/admin/presentation'
import { useState } from 'react'
import { api, ApiError } from '../lib/api'
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
  money,
  type Page,
} from '../features/workspace/ui'
import type {
  Order,
  OrderDetail as OrderView,
  Credit,
  Refund,
} from '../features/admin/types'
export default function AdminOrders() {
  const [tab, setTab] = useState('orders'),
    [status, setStatus] = useState(''),
    [kw, setKw] = useState(''),
    [page, setPage] = useState(1),
    [selected, setSelected] = useState('')
  const q = useResource<Page<Order & Credit>>(
    `/admin/${tab}?page=${page}&kw=${encodeURIComponent(kw)}${tab === 'orders' ? `&status=${encodeURIComponent(status)}` : ''}`,
  )
  return (
    <div className="ws-page">
      <PageHeader
        title="订单与积分"
        description="分别核对渠道支付、积分入账和创作续跑。"
      />
      <div className="ws-tabs" role="tablist" aria-label="订单与积分">
        <button
          role="tab"
          aria-selected={tab === 'orders'}
          onClick={() => {
            setTab('orders')
            setPage(1)
          }}
        >
          充值订单
        </button>
        <button
          role="tab"
          aria-selected={tab === 'credits'}
          onClick={() => {
            setTab('credits')
            setPage(1)
          }}
        >
          积分流水
        </button>
      </div>
      <section className="ws-section">
        <Filters
          keyword={kw}
          onKeyword={(v) => {
            setKw(v)
            setPage(1)
          }}
          status={status}
          onStatus={
            tab === 'orders'
              ? (v) => {
                  setStatus(v)
                  setPage(1)
                }
              : undefined
          }
          label="订单状态"
          statuses={[
            ['pending', '待支付'],
            ['paid', '已支付'],
            ['closed', '已关闭'],
            ['expired', '已过期'],
            ['failed', '失败'],
            ['rejected', '已驳回'],
          ]}
        />
        <State
          {...q}
          empty={q.data?.items.length === 0}
          emptyLabel={tab === 'orders' ? '暂无订单' : '暂无积分流水'}
        />
        {q.data &&
          !!q.data.items.length &&
          (tab === 'orders' ? (
            <Table
              head={[
                '订单 / 用户',
                '金额 / 积分',
                '支付状态',
                '积分入账',
                '时间',
                '操作',
              ]}
            >
              {q.data.items.map((o) => (
                <tr key={o.id}>
                  <Cell label="订单 / 用户">
                    <b className="ws-id">{o.id}</b>
                    <small>{o.userId}</small>
                  </Cell>
                  <Cell label="金额 / 积分">
                    <b>{money(o.amountCents)}</b>
                    <small>{o.credits} 积分</small>
                  </Cell>
                  <Cell label="支付状态">
                    <Status value={o.status} context="payment" />
                  </Cell>
                  <Cell label="积分入账">
                    {o.credited ? '已入账' : '未入账'}
                  </Cell>
                  <Cell label="时间">{day(o.createdAt)}</Cell>
                  <Cell label="操作">
                    <button
                      className="btn btn-sm"
                      aria-label={`查看订单 ${o.id}`}
                      onClick={() => setSelected(o.id)}
                    >
                      查看详情
                    </button>
                  </Cell>
                </tr>
              ))}
            </Table>
          ) : (
            <CreditTable items={q.data.items} />
          ))}
        {q.data && (
          <Pager
            page={page}
            total={q.data.total}
            pageSize={q.data.pageSize}
            onPage={setPage}
            disabled={q.loading}
          />
        )}
      </section>
      {selected && (
        <OrderDetail
          id={selected}
          onClose={() => setSelected('')}
          onChanged={q.retry}
        />
      )}
    </div>
  )
}
export function CreditTable({ items }: { items: Credit[] }) {
  return (
    <Table head={['用户', '变动积分', '变动后余额', '类型 / 备注', '时间']}>
      {items.map((c) => (
        <tr key={c.id}>
          <Cell label="用户">{c.userEmail || c.userId}</Cell>
          <Cell label="变动积分">
            <b className={c.amount < 0 ? 'ws-negative' : 'ws-positive'}>
              {c.amount > 0 ? '+' : ''}
              {c.amount}
            </b>
          </Cell>
          <Cell label="变动后余额">{c.balanceAfter}</Cell>
          <Cell label="类型 / 备注">
            {c.kind}
            <small>{c.note}</small>
            <small>操作人 {c.operatorId || '系统'}</small>
          </Cell>
          <Cell label="时间">{day(c.createdAt)}</Cell>
        </tr>
      ))}
    </Table>
  )
}
function OrderDetail({
  id,
  onClose,
  onChanged,
}: {
  id: string
  onClose: () => void
  onChanged: () => void
}) {
  const q = useResource<OrderView>(
      `/admin/orders/${encodeURIComponent(id)}/detail`,
    ),
    a = useAction(),
    d = q.data
  const [reason, setReason] = useState(''),
    [amount, setAmount] = useState(''),
    [note, setNote] = useState(''),
    [refundOverride, setRefund] = useState<Refund | null>(null),
    [uncertain, setUncertain] = useState(false)
  const refund = refundOverride || d?.refund
  const refresh = () => {
    q.retry()
    onChanged()
  }
  const query = () =>
    a.run(
      () => api.post<Refund>(`/admin/orders/${id}/refund/query`, {}),
      '已查询渠道退款结果',
      (r) => {
        setRefund(r)
        setUncertain(false)
        onChanged()
      },
    )
  return (
    <Detail title={`订单 ${id}`} onClose={onClose}>
      <State {...q} />
      {d && (
        <>
          <Facts
            items={[
              ['用户', d.user.email],
              ['订单 ID', d.order.id],
              ['金额', money(d.order.amountCents)],
              [
                '渠道',
                d.order.channel === 'native'
                  ? '微信支付'
                  : d.order.channel || '人工审核',
              ],
              ['支付状态', <Status value={d.order.status} context="payment" />],
              ['积分入账', d.order.credited ? '已入账' : '未入账'],
              ['关联执行', d.run?.id || d.order.runId || '无关联执行'],
              [
                '执行状态',
                d.run ? <Status value={d.run.status} /> : '无关联执行',
              ],
              ['执行检查点', d.run?.checkpoint || '—'],
              ['下单时间', day(d.order.createdAt)],
            ]}
          />
          <Feedback action={a} />
          {['native', 'h5'].includes(d.order.channel) && (
            <section className="ws-section">
              <h3>核对支付</h3>
              <p>向支付渠道查询真实结果，已支付、已入账与续跑状态分别记录。</p>
              <button
                className="btn"
                disabled={a.busy}
                onClick={() =>
                  a.run(
                    () => api.post(`/admin/orders/${id}/confirm`, {}),
                    '渠道支付查询完成',
                    refresh,
                  )
                }
              >
                {a.busy ? '查询中…' : '查询支付结果'}
              </button>
            </section>
          )}
          {d.order.status === 'pending' &&
            (!d.order.channel || d.order.channel === 'manual') && (
              <section className="ws-section">
                <h3>人工充值审核</h3>
                <label className="ws-field">
                  审核备注
                  <input
                    className="input"
                    value={note}
                    onChange={(e) => setNote(e.target.value)}
                  />
                </label>
                <div className="ws-actions">
                  <button
                    className="btn btn-primary"
                    disabled={a.busy || !note.trim()}
                    onClick={() =>
                      a.run(
                        () =>
                          api.post(`/admin/orders/${id}/handle`, {
                            approve: true,
                            note,
                          }),
                        '人工订单已通过并入账',
                        refresh,
                      )
                    }
                  >
                    通过人工订单
                  </button>
                  <button
                    className="btn"
                    disabled={a.busy || !note.trim()}
                    onClick={() =>
                      a.run(
                        () =>
                          api.post(`/admin/orders/${id}/handle`, {
                            approve: false,
                            note,
                          }),
                        '人工订单已驳回',
                        refresh,
                      )
                    }
                  >
                    驳回人工订单
                  </button>
                </div>
              </section>
            )}
          <section className="ws-section">
            <h3>退款核对</h3>
            {refund ? (
              <>
                <Status value={refund.status} prefix="退款" />
                <Facts
                  items={[
                    ['退款标识', refund.refundNo],
                    ['退款金额', money(refund.amountCents)],
                    ['回收积分', refund.creditsRevoked],
                    ['积分不足额', refund.creditShortfall],
                    ['退款原因', refund.reason],
                    ['渠道错误', refund.error || '—'],
                    ['最后更新', day(refund.updatedAt)],
                  ]}
                />
                <p>
                  退款受理不等于成功；仅渠道核验成功后回收积分。固定标识避免重复退款。
                </p>
                <button className="btn" disabled={a.busy} onClick={query}>
                  查询退款结果
                </button>
              </>
            ) : d.order.refundedAt || d.order.refunded_at ? (
              <p className="ws-error" role="status">
                历史退款记录待渠道核验，不能据此确认退款成功。
              </p>
            ) : d.order.status === 'paid' &&
              d.order.credited &&
              ['native', 'h5'].includes(d.order.channel) &&
              !d.order.mock ? (
              <>
                <p>
                  每单保留一个固定退款意图。提交后结果未知时先查询，不能重复发起；积分回收以当前余额为上限，不足额单独记录。
                </p>
                <label className="ws-field">
                  退款金额（分）
                  <input
                    className="input"
                    type="number"
                    min="1"
                    step="1"
                    value={amount || d.order.amountCents}
                    disabled={uncertain || a.busy}
                    onChange={(e) => setAmount(e.target.value)}
                  />
                </label>
                <label className="ws-field">
                  退款原因
                  <input
                    className="input"
                    value={reason}
                    maxLength={80}
                    disabled={uncertain || a.busy}
                    onChange={(e) => setReason(e.target.value)}
                  />
                </label>
                {uncertain ? (
                  <>
                    <p className="ws-error">
                      退款提交结果待确认，请先查询结果。
                    </p>
                    <button className="btn" disabled={a.busy} onClick={query}>
                      查询退款结果
                    </button>
                  </>
                ) : (
                  <button
                    className="btn btn-primary"
                    disabled={
                      a.busy ||
                      !reason.trim() ||
                      !Number.isSafeInteger(
                        Number(amount || d.order.amountCents),
                      ) ||
                      Number(amount || d.order.amountCents) <= 0 ||
                      Number(amount || d.order.amountCents) >
                        d.order.amountCents
                    }
                    onClick={() => {
                      setUncertain(true)
                      a.run(
                        () =>
                          api.post<Refund>(`/admin/orders/${id}/refund`, {
                            amountCents: Number(amount || d.order.amountCents),
                            reason: reason.trim(),
                          }),
                        '退款请求已受理，请以查询结果为准',
                        (r) => {
                          setRefund(r)
                          setUncertain(false)
                          onChanged()
                        },
                        (error) => {
                          if (
                            error instanceof ApiError &&
                            [400, 404].includes(error.status)
                          )
                            setUncertain(false)
                        },
                      )
                    }}
                  >
                    {a.busy ? '提交中…' : '提交退款'}
                  </button>
                )}
              </>
            ) : (
              <p className="ws-muted">
                {d.order.status === 'paid' && !d.order.credited
                  ? '积分尚未入账，请先核对入账状态。'
                  : d.order.status === 'paid'
                    ? '此订单无法直接通过真实渠道退款，请核对渠道和订单记录。'
                    : '订单尚未支付，无可退金额。'}
              </p>
            )}
          </section>
          <Timeline items={d.events} />
        </>
      )}
    </Detail>
  )
}

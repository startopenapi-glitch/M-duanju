import {Status} from './presentation'
import { useEffect, useRef, useState } from 'react'
import { api } from '../../lib/api'
import { usePartnerIdentity } from '../workspace/identity'
import { useResource, useAction } from '../workspace/resource'
import {
  PageHeader,
  State,
  Pager,
  Facts,
  Feedback,
  Table,
  Cell,
  day,
  type Page,
} from '../workspace/ui'
import type { Partner as PartnerType } from '../admin/types'
import { PromotionQR } from './PromotionQR'
type Overview = {
  newUsers: number
  projectUsers: number
  firstGenUsers: number
  payingUsers: number
  dataAsOf: string
  commissionOn: boolean
}
type Customer = {
  alias: string
  registeredAt: string
  hasProject: boolean
  hasGenerated: boolean
  hasPaid: boolean
}
type Order = {
  maskedId: string
  amountYuan: string
  channel: string
  status: string
  refundHint?: string
  createdAt: string
}
const rate = (n: number, d: number) =>
  d > 0 ? `${Math.round((n / d) * 100)}%` : '—'
export default function Partner({ view = 'overview' }: { view?: string }) {
  const identity = usePartnerIdentity(),
    me = identity.data
  if (identity.loading || identity.error) return <State {...identity} />
  if (!me)
    return (
      <div className="ws-state">
        <b>你的账号尚未开通合作伙伴。</b>
        <p>请联系管理员开通后使用推广工作台。</p>
      </div>
    )
  if (me.status !== 'active')
    return (
      <div className="ws-state">
        <b>推广合作已暂停</b>
        <p>推广数据与操作暂不可用，请联系管理员。</p>
      </div>
    )
  return (
    <PartnerContent
      key={`${me.id}:${view}`}
      me={me as PartnerType}
      view={view}
      refresh={identity.retry}
    />
  )
}
function PartnerContent({
  me: initialMe,
  view,
  refresh,
}: {
  me: PartnerType
  view: string
  refresh: () => void
}) {
  const [me, setMe] = useState(initialMe)
  const [page, setPage] = useState(1),
    [copyError, setCopyError] = useState(''),
    [copied, setCopied] = useState(false)
  const title =
    {
      overview: '推广概览',
      customers: '我的客户',
      orders: '充值记录',
      settings: '推广设置',
    }[view] || '推广概览'
  const path =
    view === 'settings'
      ? null
      : view === 'overview'
        ? '/partner/overview'
        : `/partner/${view}?page=${page}`
  const q = useResource<Overview & Page<Customer & Order>>(path),
    a = useAction()
  const [edit, setEdit] = useState({
    name: me.name,
    intro: me.intro,
    wechat: me.wechat,
  })
  const link = `${location.origin}/p/${me.code}`
  const input = useRef<HTMLInputElement>(null),
    alive = useRef(true)
  useEffect(
    () => () => {
      alive.current = false
    },
    [],
  )
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(link)
      if (alive.current) {
        setCopied(true)
        setCopyError('')
      }
    } catch {
      if (alive.current) {
        setCopyError('复制失败，请选中链接后手动复制。')
        input.current?.focus()
        input.current?.select()
      }
    }
  }
  return (
    <div className="ws-page">
      <PageHeader
        title={title}
        description="查看客户归属与充值记录，管理公开推广资料。"
      >
        <span className="ws-status warn">分佣未开启</span>
      </PageHeader>
      {view === 'settings' ? (
        <section className="ws-section">
          <h2>公开推广资料</h2>
          <Facts
            items={[
              ['推广码', me.code],
              ['当前名称', me.name],
              ['状态', '已开通'],
            ]}
          />
          <label className="ws-field">
            合作方名称
            <input
              className="input"
              value={edit.name}
              onChange={(e) => setEdit({ ...edit, name: e.target.value })}
            />
          </label>
          <label className="ws-field">
            公开客服微信
            <input
              className="input"
              value={edit.wechat}
              onChange={(e) => setEdit({ ...edit, wechat: e.target.value })}
            />
          </label>
          <label className="ws-field">
            简介
            <textarea
              className="input"
              maxLength={80}
              value={edit.intro}
              onChange={(e) => setEdit({ ...edit, intro: e.target.value })}
            />
          </label>
          <Feedback action={a} />
          <button
            className="btn btn-primary"
            disabled={a.busy || !edit.name.trim()}
            onClick={() =>
              a.run(
                () =>
                  api.patch('/partner/me', { ...edit, version: me.version }),
                '推广资料已保存',
                (updated: PartnerType) => {
                  setMe(updated)
                  setEdit({
                    name: updated.name,
                    intro: updated.intro,
                    wechat: updated.wechat,
                  })
                },
              )
            }
          >
            {a.busy ? '保存中…' : '保存资料'}
          </button>
          <p className="ws-muted">
            资料保存后展示在推广页。若版本冲突，请刷新后核对。
          </p>
          <button className="btn" onClick={refresh}>
            刷新资料
          </button>
        </section>
      ) : (
        <>
          <State
            {...q}
            empty={!!q.data && view !== 'overview' && !q.data.items.length}
            emptyLabel={view === 'customers' ? '还没有客户' : '还没有充值记录'}
          />
          {q.data && view === 'overview' && (
            <>
              <section className="ws-promotion ws-section">
                <div>
                  <span className="ws-eyebrow">邀请客户</span>
                  <h2>让创作从这里开始</h2>
                  <p>分享专属链接，客户注册后会记录推广归属。</p>
                  <label className="ws-field">
                    我的推广链接
                    <input
                      ref={input}
                      className="input ws-id"
                      value={link}
                      readOnly
                      onFocus={(e) => e.target.select()}
                    />
                  </label>
                  <div className="ws-actions">
                    <button className="btn btn-primary" onClick={copy}>
                      {copied ? '已复制链接' : '复制链接'}
                    </button>
                    <a
                      className="btn"
                      href={link}
                      target="_blank"
                      rel="noreferrer"
                    >
                      预览推广页
                    </a>
                  </div>
                  {copyError && (
                    <p role="alert" className="ws-error">
                      {copyError}
                    </p>
                  )}
                </div>
                <PromotionQR value={link} />
              </section>
              <div className="ws-metrics">
                {[
                  ['新增注册账号', q.data.newUsers, ''],
                  [
                    '创建过项目',
                    q.data.projectUsers,
                    rate(q.data.projectUsers, q.data.newUsers),
                  ],
                  [
                    '完成首次生成',
                    q.data.firstGenUsers,
                    rate(q.data.firstGenUsers, q.data.newUsers),
                  ],
                  [
                    '有充值记录',
                    q.data.payingUsers,
                    rate(q.data.payingUsers, q.data.newUsers),
                  ],
                ].map(([label, value, sub]) => (
                  <article className="ws-metric" key={label}>
                    <span>{label}</span>
                    <strong>{value}</strong>
                    <small>{sub}</small>
                  </article>
                ))}
              </div>
              <p className="ws-muted">
                数据截至 {day(q.data.dataAsOf)}（Asia/Shanghai）
              </p>
            </>
          )}
          {q.data && view === 'customers' && !!q.data.items.length && (
            <section className="ws-section">
              <Table
                head={['客户', '注册时间', '创建项目', '首次生成', '有充值']}
              >
                {q.data.items.map((c) => (
                  <tr key={c.alias}>
                    <Cell label="客户">
                      <b>{c.alias}</b>
                    </Cell>
                    <Cell label="注册时间">{day(c.registeredAt)}</Cell>
                    <Cell label="创建项目">{c.hasProject ? '是' : '—'}</Cell>
                    <Cell label="首次生成">{c.hasGenerated ? '是' : '—'}</Cell>
                    <Cell label="有充值">{c.hasPaid ? '是' : '—'}</Cell>
                  </tr>
                ))}
              </Table>
            </section>
          )}
          {q.data && view === 'orders' && !!q.data.items.length && (
            <section className="ws-section">
              <Table head={['订单', '金额（元）', '渠道', '状态', '时间']}>
                {q.data.items.map((o) => (
                  <tr key={o.maskedId}>
                    <Cell label="订单">{o.maskedId}</Cell>
                    <Cell label="金额（元）">{o.amountYuan}</Cell>
                    <Cell label="渠道">
                      {o.channel === 'native'
                        ? '微信支付'
                        : o.channel || '人工审核'}
                    </Cell>
                    <Cell label="状态">
                      <Status value={o.status} context="payment" />
                      <small>{o.refundHint}</small>
                    </Cell>
                    <Cell label="时间">{day(o.createdAt)}</Cell>
                  </tr>
                ))}
              </Table>
              <p className="ws-muted">
                金额为系统记录已支付金额；售后结果以客服核对为准。
              </p>
            </section>
          )}
          {q.data && view !== 'overview' && (
            <Pager
              page={page}
              total={q.data.total}
              pageSize={q.data.pageSize}
              onPage={setPage}
            />
          )}
        </>
      )}
    </div>
  )
}

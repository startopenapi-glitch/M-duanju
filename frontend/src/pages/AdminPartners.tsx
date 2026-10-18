import { Status } from '../features/admin/presentation'
import { useState } from 'react'
import { api } from '../lib/api'
import { useResource, useAction } from '../features/workspace/resource'
import {
  PageHeader,
  State,
  Pager,
  Filters,
  Feedback,
  Table,
  Cell,
  type Page,
} from '../features/workspace/ui'
import type {
  Partner,
  PartnerInvitation,
} from '../features/admin/types'

const inviteLabels: Record<string, string> = {
  pending: '待接受',
  accepted: '已接受',
  revoked: '已撤销',
  expired: '已过期',
}

function dateText(value: string) {
  if (!value) return '—'
  try {
    return new Date(value).toLocaleString('zh-CN')
  } catch {
    return value
  }
}

export default function AdminPartners() {
  const [kw, setKw] = useState(''),
    [status, setStatus] = useState(''),
    [page, setPage] = useState(1),
    [inviteOpen, setInviteOpen] = useState(false),
    [directOpen, setDirectOpen] = useState(false),
    [inviteKw, setInviteKw] = useState(''),
    [inviteStatus, setInviteStatus] = useState('pending'),
    [invitePage, setInvitePage] = useState(1)
  const [form, setForm] = useState({
    userId: '',
    name: '',
    intro: '',
    wechat: '',
  })
  const [invite, setInvite] = useState({
    email: '',
    wechat: '',
  })
  const q = useResource<Page<Partner>>(
      `/admin/partners?kw=${encodeURIComponent(kw)}&status=${encodeURIComponent(status)}&page=${page}`,
    ),
    inv = useResource<Page<PartnerInvitation>>(
      `/admin/partner-invitations?kw=${encodeURIComponent(inviteKw)}&status=${encodeURIComponent(inviteStatus)}&page=${invitePage}`,
    ),
    a = useAction()

  const sendInvite = () =>
    a.run(
      () => api.post('/admin/partner-invitations', invite),
      '邀请邮件已发送；对方登录/注册后即可接受',
      () => {
        setInvite({ email: '', wechat: '' })
        inv.retry()
      },
      () => inv.retry(),
    )

  const resend = (id: string) =>
    a.run(
      () => api.post(`/admin/partner-invitations/${id}/resend`),
      '邀请已重发，旧链接已失效',
      inv.retry,
      () => inv.retry(),
    )

  const revoke = (id: string) =>
    a.run(
      () => api.post(`/admin/partner-invitations/${id}/revoke`),
      '邀请已撤销',
      inv.retry,
    )

  return (
    <div className="ws-page">
      <PageHeader
        title="推广合作"
        description="按邮箱发出代理邀请，对方登录/注册并接受后开通；也可从已有用户直接开通。"
      >
        <button
          className="btn btn-primary"
          onClick={() => {
            setInviteOpen(!inviteOpen)
            setDirectOpen(false)
          }}
        >
          {inviteOpen ? '收起邀请表单' : '邀请合作伙伴'}
        </button>
        <button
          className="btn"
          onClick={() => {
            setDirectOpen(!directOpen)
            setInviteOpen(false)
          }}
        >
          {directOpen ? '收起直接开通' : '已有用户直接开通'}
        </button>
      </PageHeader>
      <Feedback action={a} />

      {inviteOpen && (
        <section className="ws-section">
          <h2>发送代理邀请</h2>
          <p>
            只需填写对方邮箱和公开客服微信，系统发送邀请邮件。对方须使用该邮箱登录/注册后接受；
            链接 7 天内有效，接受后才会开通合作身份，不会提前授予权限。
            合作方名称默认取邮箱前缀，接受后可在推广设置中修改。
          </p>
          <div className="ws-form-grid">
            {[
              ['email', '受邀邮箱'],
              ['wechat', '公开客服微信'],
            ].map(([field, label]) => (
              <label className="ws-field" key={field}>
                {label}
                <input
                  className="input"
                  type={field === 'email' ? 'email' : undefined}
                  value={invite[field as keyof typeof invite]}
                  onChange={(e) =>
                    setInvite({ ...invite, [field]: e.target.value })
                  }
                />
              </label>
            ))}
          </div>
          <button
            className="btn btn-primary"
            disabled={a.busy || !invite.email.trim() || !invite.wechat.trim()}
            onClick={sendInvite}
          >
            {a.busy ? '发送中…' : '发送邀请邮件'}
          </button>
        </section>
      )}

      {directOpen && (
        <section className="ws-section">
          <h2>从已有用户直接开通</h2>
          <p>
            合作身份独立于管理员权限；同一用户最多一个身份。当前分佣未开启。
            这是兼容入口，正常情况下优先使用邮件邀请。
          </p>
          <div className="ws-form-grid">
            {[
              ['userId', '用户 ID'],
              ['name', '合作方名称'],
              ['wechat', '公开客服微信'],
              ['intro', '简介（最多 80 字）'],
            ].map(([field, label]) => (
              <label className="ws-field" key={field}>
                {label}
                <input
                  className="input"
                  maxLength={field === 'intro' ? 80 : undefined}
                  value={form[field as keyof typeof form]}
                  onChange={(e) =>
                    setForm({ ...form, [field]: e.target.value })
                  }
                />
              </label>
            ))}
          </div>
          <button
            className="btn btn-primary"
            disabled={a.busy || !form.userId.trim() || !form.name.trim()}
            onClick={() =>
              a.run(
                () => api.post('/admin/partners', form),
                '合作伙伴已开通',
                () => {
                  setForm({ userId: '', name: '', intro: '', wechat: '' })
                  setDirectOpen(false)
                  q.retry()
                },
              )
            }
          >
            {a.busy ? '开通中…' : '确认开通'}
          </button>
        </section>
      )}

      <section className="ws-section">
        <h2>代理邀请</h2>
        <Filters
          keyword={inviteKw}
          onKeyword={(v) => {
            setInviteKw(v)
            setInvitePage(1)
          }}
          status={inviteStatus}
          onStatus={(v) => {
            setInviteStatus(v)
            setInvitePage(1)
          }}
          label="邀请状态"
          statuses={[
            ['pending', '待接受'],
            ['accepted', '已接受'],
            ['expired', '已过期'],
            ['revoked', '已撤销'],
          ]}
        />
        <State
          {...inv}
          empty={inv.data?.items.length === 0}
          emptyLabel="还没有代理邀请"
        />
        {inv.data && !!inv.data.items.length && (
          <Table
            head={['受邀邮箱 / 合作方', '状态', '有效期', '发送', '操作']}
          >
            {inv.data.items.map((v) => (
              <tr key={v.id}>
                <Cell label="受邀邮箱 / 合作方">
                  <b>{v.email}</b>
                  <small>{v.name}</small>
                  <small>{v.wechat || '—'}</small>
                </Cell>
                <Cell label="状态">
                  <span>{inviteLabels[v.status] || v.status}</span>
                  {v.status === 'accepted' && v.acceptedAt && (
                    <small>{dateText(v.acceptedAt)}</small>
                  )}
                </Cell>
                <Cell label="有效期">{dateText(v.expiresAt)}</Cell>
                <Cell label="发送">
                  <small>{v.sendCount} 次</small>
                  <small>{dateText(v.lastSentAt)}</small>
                </Cell>
                <Cell label="操作">
                  {(v.status === 'pending' || v.status === 'expired') && (
                    <button
                      className="btn btn-sm"
                      disabled={a.busy}
                      onClick={() => resend(v.id)}
                    >
                      重发
                    </button>
                  )}
                  {v.status === 'pending' && (
                    <button
                      className="btn btn-sm"
                      disabled={a.busy}
                      onClick={() => revoke(v.id)}
                    >
                      撤销
                    </button>
                  )}
                </Cell>
              </tr>
            ))}
          </Table>
        )}
        {inv.data && (
          <Pager
            page={invitePage}
            total={inv.data.total}
            pageSize={inv.data.pageSize}
            onPage={setInvitePage}
          />
        )}
      </section>

      <section className="ws-section">
        <h2>已开通合作方</h2>
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
          label="合作状态"
          statuses={[
            ['active', '正常'],
            ['suspended', '已暂停'],
          ]}
        />
        <State
          {...q}
          empty={q.data?.items.length === 0}
          emptyLabel="还没有合作伙伴"
        />
        {q.data && !!q.data.items.length && (
          <Table
            head={[
              '合作方 / 用户',
              '推广码',
              '公开联系',
              '状态 / 版本',
              '操作',
            ]}
          >
            {q.data.items.map((p) => (
              <tr key={p.id}>
                <Cell label="合作方 / 用户">
                  <b>{p.name}</b>
                  <small>{p.userId}</small>
                  <small>{p.intro}</small>
                </Cell>
                <Cell label="推广码">
                  <span className="ws-id">{p.code}</span>
                </Cell>
                <Cell label="公开联系">{p.wechat || '—'}</Cell>
                <Cell label="状态 / 版本">
                  <Status value={p.status} />
                  <small>版本 {p.version}</small>
                </Cell>
                <Cell label="操作">
                  <button
                    className="btn btn-sm"
                    disabled={a.busy}
                    onClick={() =>
                      a.run(
                        () =>
                          api.post(`/admin/partners/${p.id}/status`, {
                            status:
                              p.status === 'active' ? 'suspended' : 'active',
                            version: p.version,
                          }),
                        p.status === 'active' ? '合作已暂停' : '合作已恢复',
                        q.retry,
                      )
                    }
                  >
                    {p.status === 'active' ? '暂停合作' : '恢复合作'}
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
    </div>
  )
}

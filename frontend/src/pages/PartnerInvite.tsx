import { useEffect, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { PageHeader } from '../components/ui/PageHeader'
import { Feedback } from '../components/ui/Feedback'
import {
  api,
  clearToken,
  errorMessage,
  type User,
} from '../lib/api'
import { useAuth } from '../App'
import { toast } from '../lib/format'

type Lookup = {
  valid: boolean
  status: string
  email: string
  name: string
  expiresAt: string
}

export default function PartnerInvite() {
  const [params] = useSearchParams()
  const token = params.get('token') || ''
  const [info, setInfo] = useState<Lookup | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const { user, setUser } = useAuth()
  const nav = useNavigate()

  useEffect(() => {
    let alive = true
    setError('')
    if (!token) {
      setError('邀请链接缺少 token，请使用邮件中的完整链接。')
      return
    }
    api
      .get<Lookup>(
        `/partner/invitations/lookup?token=${encodeURIComponent(token)}`,
      )
      .then((data) => {
        if (alive) setInfo(data)
      })
      .catch((e) => {
        if (alive) setError(errorMessage(e))
      })
    return () => {
      alive = false
    }
  }, [token])

  const emailMatches =
    !!info &&
    !!user &&
    user.email.trim().toLowerCase() === info.email.trim().toLowerCase()
  const here = `/partner/invite?token=${encodeURIComponent(token)}`
  const authQuery = `${encodeURIComponent(here)}&email=${encodeURIComponent(
    info?.email || '',
  )}`
  const accept = async () => {
    if (busy) return
    setBusy(true)
    setError('')
    try {
      await api.post('/partner/invitations/accept', { token })
      toast('代理身份已开通', 'ok')
      setNotice('已接受邀请，正在进入推广工作台。')
      window.setTimeout(() => nav('/partner', { replace: true }), 400)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  const switchAccount = () => {
    clearToken()
    setUser(null as User | null)
    nav(`/login?next=${authQuery}`, { replace: true })
  }

  return (
    <div className="auth-page">
      <div className="card w-full max-w-xl">
        <PageHeader
          title="接受代理邀请"
          description="使用收到邀请的邮箱登录或注册后，即可开通推广合作身份。"
        />
        <Feedback error={error} notice={notice} />
        {info?.status === 'accepted' ? (
          <div className="note note-info">
            这份邀请已经接受。若你已是该账号本人，可直接进入推广工作台。
            <div className="mt-3">
              <Link className="btn btn-primary" to="/partner">
                进入推广工作台
              </Link>
            </div>
          </div>
        ) : info ? (
          <>
            <div className="note note-info">
              <p>
                邀请开通：<b>{info.name}</b>
              </p>
              <p>
                受邀邮箱：<b>{info.email}</b>
              </p>
              <p>有效期至：{new Date(info.expiresAt).toLocaleString('zh-CN')}</p>
            </div>
            {!user ? (
              <div className="flex flex-wrap gap-3">
                <Link className="btn btn-primary" to={`/login?next=${authQuery}`}>
                  登录后接受
                </Link>
                <Link className="btn" to={`/register?next=${authQuery}`}>
                  注册并接受
                </Link>
              </div>
            ) : emailMatches ? (
              <button className="btn btn-primary" disabled={busy} onClick={accept}>
                {busy ? '开通中…' : '接受邀请并开通'}
              </button>
            ) : (
              <div className="note note-err">
                <p>
                  当前登录邮箱是 <b>{user.email}</b>，与邀请邮箱不一致。
                  为避免把代理身份开给错误账号，请使用邀请邮箱登录。
                </p>
                <button className="btn" onClick={switchAccount}>
                  退出并使用邀请邮箱登录
                </button>
              </div>
            )}
          </>
        ) : (
          <p role="status">正在核对邀请…</p>
        )}
        <p className="text-sm text-ink3 mt-6">
          邀请链接 7 天内有效；接受后只开通推广身份，不会改变管理员权限、价格或积分规则。
        </p>
      </div>
    </div>
  )
}

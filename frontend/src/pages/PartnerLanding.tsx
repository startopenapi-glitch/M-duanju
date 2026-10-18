import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, errorMessage } from '../lib/api'

type Profile = { code: string; name: string; intro: string; wechat: string; usable: boolean; reason?: string }

/**
 * 推广落地页 /p/:code —— 公开页面，不要求登录。
 * 只展示服务端返回的公开资料，不渲染任何代理自定义 HTML/脚本/跳转地址。
 * 来源归属由服务端在响应中下发的签名 cookie 决定，本页不携带任何归属参数。
 */
export default function PartnerLanding() {
  const { code = '' } = useParams()
  const [p, setP] = useState<Profile | null>(null)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let alive = true
    setLoading(true)
    api.get<Profile>(`/partner/public/${encodeURIComponent(code)}`)
      .then(d => { if (alive) { setP(d); setErr('') } })
      .catch(e => { if (alive) setErr(errorMessage(e)) })
      .finally(() => { if (alive) setLoading(false) })
    return () => { alive = false }
  }, [code])

  if (loading) return <main className="min-h-screen grid place-items-center text-ink3">加载中…</main>

  return (
    <main className="min-h-screen flex items-center justify-center px-5 py-10">
      <div className="w-full max-w-md">
        <div className="text-center mb-6">
          <h1 className="text-2xl font-bold tracking-tight">M-duanju</h1>
          <div className="text-xs text-ink3 mt-1">AI 短剧创作平台</div>
        </div>

        {err && (
          <div className="note note-err mb-4" role="alert">
            这个推广链接不可用（可能已失效）。你仍然可以正常注册并使用平台。
          </div>
        )}

        {p && (
          <section className="card mb-4">
            <div className="text-xs text-ink3 mb-1">推广伙伴</div>
            <div className="text-xl font-bold">{p.name}</div>
            {p.intro && <p className="text-sm text-ink2 mt-2 leading-relaxed whitespace-pre-wrap">{p.intro}</p>}
            {p.wechat && (
              <div className="mt-3 text-sm text-ink2">
                客服微信：<b className="select-all">{p.wechat}</b>
              </div>
            )}
            {!p.usable && p.reason && (
              <div className="note note-info mt-3">{p.reason}</div>
            )}
          </section>
        )}

        <section className="card mb-5">
          <h3 className="font-bold mb-2">你能做什么</h3>
          <ul className="text-sm text-ink2 space-y-1.5 leading-relaxed">
            <li>· 输入一句创意，自动生成剧本、人物定妆照与分镜画面</li>
            <li>· 自动生成镜头并合成短片，完成后查看与修改</li>
            <li>· 按积分计费，用多少扣多少，不订阅不包月</li>
          </ul>
        </section>

        <div className="flex flex-col gap-2.5">
          <Link className="btn btn-primary btn-block" to="/register">
            注册开始创作
          </Link>
          <Link className="btn btn-block" to="/login">已有账号登录</Link>
        </div>

        <p className="text-[11px] text-ink3 text-center mt-5 leading-relaxed">
          推广来源仅用于统计，不影响你的价格与权益。
        </p>
      </div>
    </main>
  )
}

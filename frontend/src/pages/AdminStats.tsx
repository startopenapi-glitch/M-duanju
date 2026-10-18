import { Link } from 'react-router-dom'
import { useResource } from '../features/workspace/resource'
import { PageHeader, State, day, money } from '../features/workspace/ui'
type Overview = {
  users: number
  projects: number
  completed: number
  pendingOrders: number
  totalRechargedCents: number
  totalCredits: number
  activeRuns: number
  unknownJobs: number
  failedRuns: number
  refundPending: number
  asOf: string
}
export default function AdminStats() {
  const q = useResource<Overview>('/admin/overview'),
    s = q.data
  const cards = s
    ? [
        ['注册用户', s.users, '人'],
        ['创作项目', s.projects, '个'],
        ['已完成成片', s.completed, '个'],
        ['累计充值', money(s.totalRechargedCents), '已支付金额'],
        ['积分总余额', s.totalCredits, '积分'],
        ['进行中执行', s.activeRuns, '次'],
      ]
    : []
  return (
    <div className="ws-page">
      <PageHeader
        title="运营概览"
        description="查看用户、创作和充值的当前事实。"
      />
      <State {...q} />
      {s && (
        <>
          <div className="ws-metrics">
            {cards.map(([label, value, unit]) => (
              <article className="ws-metric" key={label}>
                <span>{label}</span>
                <strong>{value}</strong>
                <small>{unit}</small>
              </article>
            ))}
          </div>
          <section className="ws-section">
            <h2>待处理事项</h2>
            <div className="ws-attention">
              {[
                ['待支付订单', s.pendingOrders, '/admin/orders'],
                ['待核对提交', s.unknownJobs, '/admin/jobs'],
                ['失败执行', s.failedRuns, '/admin/jobs'],
                ['退款待确认', s.refundPending, '/admin/orders'],
              ].map(([label, value, to]) => (
                <Link key={label} to={String(to)}>
                  <span>{label}</span>
                  <b>{value}</b>
                  <span aria-hidden>↗</span>
                </Link>
              ))}
            </div>
          </section>
          <p className="ws-muted">
            数据截至 {day(s.asOf)}
            （Asia/Shanghai）。充值收入为渠道已支付金额，未扣除退款和生成成本。
          </p>
        </>
      )}
    </div>
  )
}

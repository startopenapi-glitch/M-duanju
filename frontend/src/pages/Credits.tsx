import { useEffect, useRef } from "react";
import {
  useInRouterContext,
  useNavigate,
  useSearchParams,
} from "react-router-dom";
import { PageHeader } from "../components/ui/PageHeader";
import { Feedback } from "../components/ui/Feedback";
import { ResponsiveList } from "../components/ui/ResponsiveList";
import PayQRCode from "../components/PayQRCode";
import { useRechargeFlow } from "../features/billing/useRechargeFlow";
import { presentRecharge } from "../features/billing/presentation";
import type { RechargeOrder } from "../features/billing/types";

const PACKS = [
  { yuan: 100, get: 10000, label: "体验包", discount: "", bonus: 0, note: "可生成约 250 秒（720P）" },
  { yuan: 500, get: 66667, label: "基础包", discount: "7.5 折", bonus: 16667, note: "可生成约 1,666 秒（720P）" },
  {
    yuan: 3000,
    get: 461538,
    label: "工作室包",
    discount: "6.5 折",
    bonus: 161538,
    note: "可生成约 11,538 秒（720P）",
  },
];
const date = (v: string) => new Date(v).toLocaleString("zh-CN");
const orderLabel = (o: RechargeOrder) =>
  ({
    paid: "已记录付款",
    rejected: "已驳回",
    closed: "已关闭",
    refunded: "已退款",
  })[o.status] || (o.payMode === "manual" ? "待审核" : "待支付");
const continuation: Record<string, string> = {
  unknown: "等待核对原任务",
  superseded: "原任务已有后续版本，不会再次启动",
  checking: "原任务正在核对，请勿重复生成",
  queued: "原任务已进入队列",
  running: "原任务正在制作",
  completed: "原任务已完成",
  waiting_credits: "仍在等待足够积分或自动续跑处理",
  settlement_pending: "原任务待结算，暂不继续生成",
  cancelled: "原任务已取消",
  failed: "原任务已停止，请回项目查看原因",
};

function CreditsView({
  runId = "",
  orderId = "",
  onObserved,
}: {
  runId?: string;
  orderId?: string;
  onObserved?: (id: string) => void;
}) {
  const f = useRechargeFlow({ runId, orderId }),
    p = f.progress,
    view = p ? presentRecharge(p, f.context) : null;
  const observed = useRef("");
  useEffect(() => {
    // This is a recoverable read context, not a second payment command.
    // Preserve Router's history state when enriching the current URL.
    if (f.orderId && window.location.pathname === "/credits") {
      const url = new URL(window.location.href);
      url.searchParams.set("orderId", f.orderId);
      if (runId) url.searchParams.set("runId", runId);
      window.history.replaceState(
        window.history.state,
        "",
        url.pathname + url.search,
      );
    }
  }, [f.orderId, runId]);
  useEffect(() => {
    if (
      f.phase === "continuation-observed" &&
      f.context?.isLatest &&
      observed.current !== f.orderId
    ) {
      observed.current = f.orderId;
      onObserved?.(f.context.project.id);
    }
  }, [f.phase, f.context, f.orderId, onObserved]);
  const inspect = (o: RechargeOrder) => (
    <button className="btn btn-sm" onClick={() => f.selectOrder(o.id)}>
      查询订单
    </button>
  );
  return (
    <div className="space-y-8">
      <PageHeader
        title="积分与充值"
        description="余额、订单与原任务进度，在这里统一查看。"
      />
      <Feedback error={f.error} />
      {f.phase === "error" && (
        <button className="btn" onClick={f.retry}>
          重试读取
        </button>
      )}
      {f.phase === "creation-unknown" && (
        <div className="note note-info">
          下单结果尚未确认。请在充值记录中查询原订单，或刷新本页核对记录；不要重复下单。
        </div>
      )}
      <section
        className="card flex flex-wrap gap-6 items-center"
        aria-label="积分余额"
      >
        <div>
          <p className="text-sm text-ink2">当前余额</p>
          <p className="text-4xl font-bold tabular-nums">
            {f.balance === null ? "—" : f.balance.toLocaleString()}
          </p>
        </div>
        <p className="text-sm text-ink2">
          积分用于支付视频生成费用。<b>1 元 = 100 积分</b>。<br />
          15 秒短片（3 段，720P）约需 <b>630 积分</b>。
        </p>
      </section>
      {f.context && (
        <section className="note note-info" aria-label="本次充值关联项目">
          <p>关联项目：<b>{f.context.project.title}</b></p>
          <p className="text-xs break-all">执行编号：{f.context.run.id}</p>
        </section>
      )}
      {f.orderId && (
        <section className="card space-y-4" aria-label="原订单进度">
          <h2 className="text-lg font-bold">订单进度</h2>
          <p className="text-xs text-ink3 break-all">订单 {f.orderId}</p>
          {p && view ? (
            <>
              <dl className="grid sm:grid-cols-3 gap-3">
                <div>
                  <dt className="text-sm text-ink2">付款</dt>
                  <dd>
                    {view.payment === "manual"
                      ? p.status === "paid"
                        ? "人工审核已确认"
                        : "人工审核订单"
                      : view.payment === "recorded"
                        ? "已记录渠道付款"
                        : p.status === "closed"
                          ? "订单已关闭"
                          : "等待渠道核验"}
                  </dd>
                </div>
                <div>
                  <dt className="text-sm text-ink2">积分</dt>
                  <dd>{p.credited ? "已记录入账" : "尚未确认入账"}</dd>
                </div>
                {p.runId && (
                  <div>
                    <dt className="text-sm text-ink2">原任务</dt>
                    <dd>
                      {continuation[view.continuation] || "请查看任务当前状态"}
                    </dd>
                  </div>
                )}
              </dl>
              {view.refund && (
                <p className="note note-info">
                  订单存在退款记录（{view.refund}
                  ），请以积分流水与任务当前状态为准。
                </p>
              )}
              {p.payMode === "wechat" &&
                p.status === "pending" &&
                !p.refund && (
                  <PayQRCode
                    amount={p.amountCents / 100}
                    qr={f.qr}
                    h5Url={f.h5Url}
                  />
                )}
              {p.payMode === "manual" && p.status === "pending" && (
                <p className="note note-info">
                  申请已保留，等待管理员审核。无需重复申请。
                </p>
              )}
            </>
          ) : (
            <p role="status">正在读取订单…</p>
          )}
          {f.phase === "paused" && (
            <p role="status">
              自动查询已暂停，订单状态未改变。可继续查询原订单。
            </p>
          )}
          <div className="ui-actions">
            <button className="btn" onClick={f.check}>
              查询原订单
            </button>
            {f.context && (
              <a className="btn" href={`/project/${f.context.project.id}`}>
                返回原项目
              </a>
            )}
            <a className="btn" href="/credits">
              返回充值首页
            </a>
          </div>
        </section>
      )}
      {!f.orderId && (
        <section className="space-y-3">
          <h2 className="text-lg font-bold">选择充值套餐</h2>
          {runId && (
            <p className="note note-info">
              本次关联原任务。积分到账后，由服务端按任务当前状态自动续跑，无需再次点击生成。
            </p>
          )}
          <div className="grid md:grid-cols-3 gap-3">
            {PACKS.map((pack) => (
              <div key={pack.yuan} className="card text-center py-6 space-y-2">
                <div className="text-sm text-ink2">{pack.label}</div>
                <div className="text-3xl font-bold">¥{pack.yuan}</div>
                {pack.discount && (
                  <div className="tag">{pack.discount}</div>
                )}
                <div className="font-semibold">
                  到账 {pack.get.toLocaleString()} 积分
                </div>
                <p className="text-sm text-ink2">
                  {pack.bonus > 0
                    ? `赠送 ${pack.bonus.toLocaleString()} 积分`
                    : "无赠送"}
                </p>
                <p className="text-sm text-ink2">{pack.note}</p>
                <button
                  className="btn btn-primary btn-block"
                  disabled={f.busy || f.phase !== "ready"}
                  onClick={() => f.recharge(pack.yuan * 100)}
                >
                  {f.busy
                    ? "提交中…"
                    : f.payMode === "wechat"
                      ? "微信扫码支付"
                      : f.payMode === "manual"
                        ? "申请充值"
                        : "正在读取…"}
                </button>
              </div>
            ))}
          </div>
          {f.payMode === "manual" && (
            <p className="text-sm text-ink2">
              当前通过人工审核确认充值，提交申请后可在原订单查看进度。
            </p>
          )}
        </section>
      )}
      <section className="space-y-3">
        <h2 className="text-lg font-bold">充值记录</h2>
        <ResponsiveList
          items={f.orders}
          getKey={(o) => o.id}
          empty={f.balance === null ? "正在读取…" : "暂无记录"}
          columns={[
            { id: "time", header: "时间", cell: (o) => date(o.createdAt) },
            { id: "amount", header: "金额", cell: (o) => `¥${o.amountYuan}` },
            { id: "credits", header: "积分", cell: (o) => o.credits },
            { id: "status", header: "订单状态", cell: orderLabel },
            { id: "action", header: "操作", cell: inspect },
          ]}
          renderMobile={(o) => (
            <div className="space-y-2">
              <div className="flex justify-between gap-3">
                <b>¥{o.amountYuan}</b>
                <span>{orderLabel(o)}</span>
              </div>
              <p className="text-sm text-ink2">
                {o.credits} 积分 · {date(o.createdAt)}
              </p>
              {inspect(o)}
            </div>
          )}
        />
      </section>
      <section className="space-y-3">
        <h2 className="text-lg font-bold">积分流水</h2>
        <ResponsiveList
          items={f.transactions}
          getKey={(t) => t.id}
          empty={f.balance === null ? "正在读取…" : "暂无流水"}
          columns={[
            { id: "time", header: "时间", cell: (t) => date(t.createdAt) },
            {
              id: "change",
              header: "变动",
              cell: (t) => `${t.amount > 0 ? "+" : ""}${t.amount}`,
            },
            { id: "balance", header: "余额", cell: (t) => t.balanceAfter },
            { id: "note", header: "说明", cell: (t) => t.note || "—" },
          ]}
          renderMobile={(t) => (
            <div className="space-y-2">
              <b>
                {t.amount > 0 ? "+" : ""}
                {t.amount} 积分
              </b>
              <p>{t.note || "积分调整"}</p>
              <p className="text-sm text-ink2">
                余额 {t.balanceAfter} · {date(t.createdAt)}
              </p>
            </div>
          )}
        />
      </section>
    </div>
  );
}
function RoutedCredits() {
  const [q] = useSearchParams(),
    navigate = useNavigate();
  return (
    <CreditsView
      runId={q.get("runId") || ""}
      orderId={q.get("orderId") || ""}
      onObserved={(id) => navigate(`/project/${id}`, { replace: true })}
    />
  );
}
export default function Credits() {
  const routed = useInRouterContext();
  return routed ? <RoutedCredits /> : <CreditsView />;
}

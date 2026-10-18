import { StatusBadge } from "../../components/ui/StatusBadge";
const labels: Record<string, string> = {
  pending: "待处理",
  paid: "已支付",
  closed: "已关闭",
  expired: "已过期",
  failed: "失败",
  rejected: "已驳回",
  queued: "排队中",
  running: "执行中",
  waiting_credits: "等待积分",
  settlement_pending: "结算待确认",
  completed: "已完成",
  done: "已完成",
  abandoned: "已终止",
  APPROVED: "已验收",
  GENERATED: "已生成",
  QUEUED: "排队中",
  GENERATING: "生成中",
  FAILED: "失败",
  DRAFT: "草稿",
  settled: "已结算",
  pending_settlement: "待结算",
  legacy_unverified: "历史记录待核验",
  cancelled: "已取消",
  reconcile: "待核对",
  submitting: "提交中",
  processing: "处理中",
  unknown: "结果未知",
  succeeded: "成功",
  active: "正常",
  banned: "已停用",
  suspended: "已暂停",
};
export function Status({
  value,
  prefix = "",
  context,
}: {
  value?: string;
  prefix?: string;
  context?: "payment" | "receipt";
}) {
  const cls = [
    "paid",
    "completed",
    "done",
    "succeeded",
    "active",
    "APPROVED",
    "GENERATED",
    "settled",
  ].includes(value || "")
    ? "good"
    : ["failed", "banned", "rejected", "suspended"].includes(value || "")
      ? "bad"
      : "warn";
  const label =
    value === "pending" && context === "payment"
      ? "待支付"
      : value === "pending" && context === "receipt"
        ? "待结算"
        : value
          ? labels[value] || `未知状态（${value}）`
          : "未记录";
  return (
    <StatusBadge
      label={prefix + label}
      tone={cls === "good" ? "success" : cls === "bad" ? "danger" : "warning"}
    />
  );
}

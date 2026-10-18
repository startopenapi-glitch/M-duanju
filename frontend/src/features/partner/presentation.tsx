import { StatusBadge } from "../../components/ui/StatusBadge";
export function Status({
  value,
  context,
}: {
  value?: string;
  context?: "payment" | "receipt";
}) {
  const labels: Record<string, string> = {
    pending: context === "receipt" ? "待结算" : "待支付",
    paid: "已支付",
    closed: "已关闭",
    expired: "已过期",
    failed: "失败",
    rejected: "已驳回",
    refunded: "已退款",
  };
  return (
    <StatusBadge
      label={value ? labels[value] || `未知状态（${value}）` : "未记录"}
      tone={
        value === "paid"
          ? "success"
          : ["failed", "rejected"].includes(value || "")
            ? "danger"
            : "neutral"
      }
    />
  );
}

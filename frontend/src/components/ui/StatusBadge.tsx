export type BadgeTone = "neutral" | "info" | "success" | "warning" | "danger";
export function StatusBadge({
  label,
  tone,
}: {
  label: string;
  tone: BadgeTone;
}) {
  return <span className={`ui-badge ui-badge-${tone}`}>{label}</span>;
}

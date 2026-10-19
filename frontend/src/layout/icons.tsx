import type { IconName } from "./nav-config";

const paths: Record<IconName, string> = {
  film: "M3 5h18v14H3zM7 5v14M17 5v14M3 9h4M3 15h4M17 9h4M17 15h4",
  coin: "M12 3a9 9 0 100 18 9 9 0 000-18zM12 7v10M9.5 9.5h5",
  shield: "M12 3l7 3v6c0 4-3 7-7 9-4-2-7-5-7-9V6z",
  chart: "M4 20V10M10 20V4M16 20v-7M22 20H2",
  users:
    "M9 11a3.5 3.5 0 100-7 3.5 3.5 0 000 7zM3 20c0-3 2.7-5 6-5s6 2 6 5M17 8a3 3 0 100 6M18 20c0-2-.7-3.4-2-4.3",
  receipt: "M6 3h12v18l-3-2-3 2-3-2-3 2zM9 8h6M9 12h6",
};

export function Icon({ name, size = 16 }: { name: IconName; size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d={paths[name]} />
    </svg>
  );
}

import type { ReactNode } from "react";

// 唯一内容容器：页面不得再自带 max-w-* / min-h-screen
export function PageContainer({
  children,
  width = "default",
}: {
  children: ReactNode;
  width?: "default" | "wide" | "form";
}) {
  return <div className={`page-container page-${width}`}>{children}</div>;
}

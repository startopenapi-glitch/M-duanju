import type { ReactNode } from "react";
export function Feedback({
  error,
  notice,
  children,
}: {
  error?: string;
  notice?: string;
  children?: ReactNode;
}) {
  return (
    <>
      {error && (
        <div className="note note-err" role="alert">
          {error}
          {children}
        </div>
      )}
      {notice && (
        <div className="note note-info" role="status">
          {notice}
        </div>
      )}
    </>
  );
}

import { useEffect, useState, type ReactNode, type Key } from "react";
export function ResponsiveList<T>({
  items,
  getKey,
  columns,
  renderMobile,
  empty,
}: {
  items: T[];
  getKey: (item: T) => Key;
  columns: { id: string; header: string; cell: (item: T) => ReactNode }[];
  renderMobile: (item: T) => ReactNode;
  empty: ReactNode;
}) {
  const [mobile, set] = useState(
    () => window.matchMedia?.("(max-width: 767px)").matches ?? false,
  );
  useEffect(() => {
    const q = window.matchMedia?.("(max-width: 767px)");
    if (!q) return;
    const change = () => set(q.matches);
    q.addEventListener("change", change);
    return () => q.removeEventListener("change", change);
  }, []);
  if (!items.length) return <div className="ws-state">{empty}</div>;
  return mobile ? (
    <ul className="ui-mobile-list">
      {items.map((i) => (
        <li className="card" key={getKey(i)}>
          {renderMobile(i)}
        </li>
      ))}
    </ul>
  ) : (
    <div className="ws-table-wrap">
      <table className="ws-table">
        <thead>
          <tr>
            {columns.map((c) => (
              <th key={c.id}>{c.header}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {items.map((i) => (
            <tr key={getKey(i)}>
              {columns.map((c) => (
                <td key={c.id}>{c.cell(i)}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

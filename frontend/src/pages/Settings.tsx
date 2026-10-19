import { Navigate, useSearchParams } from "react-router-dom";
import { useAuth } from "../App";
import Security from "./Security";
export default function Settings() {
  const [q] = useSearchParams(),
    { user } = useAuth(),
    tab = q.get("tab");
  if (tab === "billing") {
    const next = new URLSearchParams();
    for (const k of ["runId", "orderId"]) {
      const v = q.get(k);
      if (v && /^[a-zA-Z0-9_-]{1,64}$/.test(v)) next.set(k, v);
    }
    return <Navigate to={"/credits" + (next.size ? "?" + next : "")} replace />;
  }
  if (tab === "admin")
    return user?.role === "admin" ? (
      <Navigate to="/admin/stats" replace />
    ) : (
      <div role="alert" className="note note-err">
        需要管理员权限
      </div>
    );
  return <Security />;
}

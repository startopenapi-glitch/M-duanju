import { NavLink, useNavigate } from "react-router-dom";
import { useAuth } from "../App";
import { clearToken } from "../lib/api";
import { usePartnerIdentity } from "../features/workspace/identity";
export function AccountMenu({ onNavigate }: { onNavigate: () => void }) {
  const { user, setUser } = useAuth(),
    partner = usePartnerIdentity(),
    nav = useNavigate();
  if (!user) return null;
  return (
    <section className="app-sidebar-bottom" aria-label="账户导航">
      <span className="app-user-mail" title={user.email}>
        {user.email}
      </span>
      <nav>
        <NavLink className="app-nav-item" to="/credits" onClick={onNavigate}>
          积分与充值
        </NavLink>
        <NavLink
          className="app-nav-item"
          to="/settings?tab=account"
          onClick={onNavigate}
        >
          账户设置
        </NavLink>
        {user.role === "admin" && (
          <NavLink
            className="app-nav-item"
            to="/admin/stats"
            onClick={onNavigate}
          >
            运营管理
          </NavLink>
        )}
        {partner.data && (
          <NavLink className="app-nav-item" to="/partner" onClick={onNavigate}>
            推广合作{partner.data.status === "suspended" ? "（已暂停）" : ""}
          </NavLink>
        )}
      </nav>
      {partner.error && (
        <div role="alert" className="text-xs">
          推广身份加载失败：{partner.error}
          <button className="btn" onClick={partner.retry}>
            重试推广身份
          </button>
        </div>
      )}
      <button
        className="app-logout"
        onClick={() => {
          clearToken();
          setUser(null);
          nav("/login");
          onNavigate();
        }}
      >
        退出
      </button>
    </section>
  );
}

import { AccountMenu } from "../../layout/AccountMenu";
import { NavLink, useNavigate } from "react-router-dom";
import {
  LayoutDashboard,
  Clapperboard,
  ReceiptText,
  Users,
  Handshake,
  ShieldCheck,
  Settings,
  ArrowLeft,
  LogOut,
  UserRound,
} from "lucide-react";
import { useAuth } from "../../App";
import { clearToken } from "../../lib/api";
import { usePartnerIdentity } from "./identity";
import { ADMIN_ROUTES } from "../../layout/nav-config";
const ADMIN_ICONS: Record<string, typeof LayoutDashboard> = {
  "/admin/stats": LayoutDashboard,
  "/admin/jobs": Clapperboard,
  "/admin/orders": ReceiptText,
  "/admin/users": Users,
  "/admin/partners": Handshake,
  "/admin/system": ShieldCheck,
};
export const ADMIN_NAV = ADMIN_ROUTES.map((route) => ({
  ...route,
  icon: ADMIN_ICONS[route.to],
}));
const PARTNER_NAV = [
  { to: "/partner", label: "推广概览", icon: LayoutDashboard },
  { to: "/partner/customers", label: "我的客户", icon: Users },
  { to: "/partner/orders", label: "充值记录", icon: ReceiptText },
  { to: "/partner/settings", label: "推广设置", icon: Settings },
];
export function WorkspaceNav({
  mode,
  onNavigate,
}: {
  mode: "admin" | "partner";
  onNavigate: () => void;
}) {
  const { user, setUser } = useAuth(),
    identity = usePartnerIdentity(),
    nav = useNavigate();
  const items =
    mode === "admin" && user?.role === "admin"
      ? ADMIN_NAV
      : mode === "partner" && identity.data?.status === "active"
        ? PARTNER_NAV
        : [];
  return (
    <aside
      className="app-sidebar ws-sidebar"
      aria-label={mode === "admin" ? "管理导航" : "推广导航"}
    >
      <div className="app-brand">
        <span className="app-brand-mark">剧</span>
        <div>
          <b>M-duanju</b>
          <small>
            {mode === "admin" ? "运营管理工作台" : "推广合作工作台"}
          </small>
        </div>
      </div>
      <div className="ws-sidebar-caption">
        {mode === "admin" ? "运营管理" : "推广合作"}
      </div>
      <nav className="app-nav">
        {items.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            end={to === "/partner"}
            to={to}
            onClick={onNavigate}
            className={({ isActive }) =>
              "app-nav-item" + (isActive ? " is-active" : "")
            }
          >
            <Icon size={18} />
            <span>{label}</span>
          </NavLink>
        ))}
      </nav>
      <div className="ws-sidebar-note">
        <ShieldCheck size={16} />
        <span>
          {mode === "admin" ? "业务操作均记录审计" : "客户资料已脱敏展示"}
        </span>
      </div>
      <NavLink to="/" onClick={onNavigate} className="app-nav-item">
        我的项目
      </NavLink>
      <AccountMenu onNavigate={onNavigate} />
    </aside>
  );
}

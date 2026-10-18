import { NavLink } from "react-router-dom";
import { visibleNav } from "./nav-filter";
import { Icon } from "./icons";

export function Sidebar({
  role,
  credits,
  email,
  onLogout,
  onNavigate,
}: {
  role?: string;
  credits?: number;
  email?: string;
  onLogout: () => void;
  onNavigate?: () => void;
}) {
  const groups = visibleNav(role);
  return (
    <aside className="app-sidebar" aria-label="侧边导航">
      <div className="app-brand">
        <span className="app-brand-mark">剧</span>
        <div>
          <b>M-duanju</b>
          <small>AI 一键成片</small>
        </div>
      </div>

      <nav className="app-nav">
        {groups.map((g, gi) => (
          <div className="app-nav-group" key={g.label ?? gi}>
            {g.label && <div className="app-nav-label">{g.label}</div>}
            {g.items.map((it) => (
              <NavLink
                key={it.to}
                to={it.to}
                end={it.to === "/"}
                onClick={onNavigate}
                className={({ isActive }) =>
                  "app-nav-item" + (isActive ? " is-active" : "")
                }
              >
                <Icon name={it.icon} />
                <span>{it.label}</span>
                {it.badge === "credits" && typeof credits === "number" && (
                  <em className="app-nav-badge">{credits}</em>
                )}
              </NavLink>
            ))}
          </div>
        ))}
      </nav>

      <div className="app-sidebar-bottom">
        <span className="app-user-mail" title={email}>
          {email}
        </span>
        <button className="app-logout" onClick={onLogout}>
          退出登录
        </button>
      </div>
    </aside>
  );
}

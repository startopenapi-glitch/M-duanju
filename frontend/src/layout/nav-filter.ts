// 纯函数：按角色过滤导航。角色规则只在这里出现。
import { NAV, type NavGroup } from "./nav-config";

export function visibleNav(role?: string): NavGroup[] {
  const isAdmin = role === "admin";
  return NAV.map((g) => ({
    ...g,
    items: g.items.filter((i) => !i.role || (i.role === "admin" && isAdmin)),
  })).filter((g) => g.items.length > 0);
}

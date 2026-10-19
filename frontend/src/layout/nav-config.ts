// 唯一导航真相源：加页面只改这里 + 路由。不得包含 JSX。
export type IconName =
  | "film"
  | "coin"
  | "shield"
  | "chart"
  | "users"
  | "receipt";
export type NavItem = {
  to: string;
  label: string;
  icon: IconName;
  role?: "admin";
  badge?: "credits";
};
export type NavGroup = { label?: string; items: NavItem[] };

export const NAV: NavGroup[] = [
  { items: [{ to: "/", label: "我的项目", icon: "film" }] },
  {
    label: "账户",
    items: [
      { to: "/credits", label: "积分", icon: "coin", badge: "credits" },
      { to: "/security", label: "安全设置", icon: "shield" },
    ],
  },
  {
    label: "管理",
    items: [
      { to: "/admin/stats", label: "运营概览", icon: "chart", role: "admin" },
      { to: "/admin/jobs", label: "创作任务", icon: "film", role: "admin" },
      {
        to: "/admin/orders",
        label: "订单与积分",
        icon: "receipt",
        role: "admin",
      },
      { to: "/admin/users", label: "用户管理", icon: "users", role: "admin" },
      {
        to: "/admin/partners",
        label: "推广合作",
        icon: "users",
        role: "admin",
      },
      {
        to: "/admin/system",
        label: "系统与审计",
        icon: "shield",
        role: "admin",
      },
    ],
  },
];

export const ADMIN_ROUTES = NAV.flatMap((group) => group.items).filter(
  (item) => item.role === "admin",
);

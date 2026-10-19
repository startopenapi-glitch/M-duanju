import { test, expect, vi, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { AccountMenu } from "./AccountMenu";
import Settings from "../pages/Settings";
const state = vi.hoisted(() => ({
  user: { id: "a", role: "user", email: "a@example.com" },
  partner: { data: null as any, error: "", loading: false, retry: vi.fn() },
}));
vi.mock("../App", () => ({
  useAuth: () => ({ user: state.user, setUser: vi.fn() }),
}));
vi.mock("../features/workspace/identity", () => ({
  usePartnerIdentity: () => state.partner,
}));
vi.mock("../pages/Security", () => ({ default: () => <h1>账户设置</h1> }));
afterEach(() => {
  state.user.role = "user";
  state.partner.data = null;
  state.partner.error = "";
  vi.restoreAllMocks();
});
test.each(["user", "partner", "admin", "suspended"])(
  "navigation_is_unique_per_role %s",
  (role) => {
    state.user.role = role === "admin" ? "admin" : "user";
    state.partner.data = ["partner", "suspended"].includes(role)
      ? { status: role === "partner" ? "active" : "suspended" }
      : null;
    render(
      <MemoryRouter>
        <AccountMenu onNavigate={() => {}} />
      </MemoryRouter>,
    );
    expect(screen.getAllByRole("link", { name: "积分与充值" })).toHaveLength(1);
    expect(screen.getAllByRole("link", { name: "账户设置" })).toHaveLength(1);
    expect(screen.queryByRole("link", { name: "运营管理" }) !== null).toBe(
      role === "admin",
    );
    expect(screen.queryByRole("link", { name: /推广合作/ }) !== null).toBe(
      ["partner", "suspended"].includes(role),
    );
  },
);
test("identity_failure_is_not_silently_ordinary", () => {
  state.partner.error = "暂时不可用";
  render(
    <MemoryRouter>
      <AccountMenu onNavigate={() => {}} />
    </MemoryRouter>,
  );
  expect(screen.getByRole("alert").textContent).toContain("暂时不可用");
  expect(screen.getByRole("button", { name: "重试推广身份" })).toBeTruthy();
});
test("legacy_settings_does_not_render_nested_billing_or_external_return", () => {
  render(
    <MemoryRouter
      initialEntries={[
        "/settings?returnTo=https://example.com&tab=unknown",
      ]}
    >
      <Settings />
    </MemoryRouter>,
  );
  expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  expect(screen.queryByRole("button", { name: "积分与充值" })).toBeNull();
  expect(screen.queryByRole("link", { name: "推广工作台" })).toBeNull();
});

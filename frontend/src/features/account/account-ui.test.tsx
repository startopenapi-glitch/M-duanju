import { test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import Login from "../../pages/Login";
import Register from "../../pages/Register";
import { api } from "../../lib/api";
afterEach(() => vi.restoreAllMocks());
test("auth_field_errors_are_accessible", async () => {
  vi.spyOn(api, "post").mockRejectedValue(new Error("登录暂不可用"));
  render(
    <MemoryRouter>
      <Login />
    </MemoryRouter>,
  );
  fireEvent.change(screen.getByLabelText("邮箱"), {
    target: { value: "test@example.invalid" },
  });
  fireEvent.change(screen.getByLabelText("密码"), {
    target: { value: "test-pass" },
  });
  fireEvent.submit(
    screen.getByRole("button", { name: "登录" }).closest("form")!,
  );
  expect((await screen.findByRole("alert")).textContent).toContain(
    "登录暂不可用",
  );
  expect(screen.getByRole("heading", { level: 1 }).textContent).toContain(
    "登录",
  );
});
test("register_mismatch_is_inline_and_does_not_send_request", async () => {
  const post = vi.spyOn(api, "post");
  render(
    <MemoryRouter>
      <Register />
    </MemoryRouter>,
  );
  fireEvent.change(screen.getByLabelText("密码（至少 6 位）"), {
    target: { value: "abcdef" },
  });
  fireEvent.change(screen.getByLabelText("确认密码"), {
    target: { value: "abcdeg" },
  });
  fireEvent.submit(
    screen.getByRole("button", { name: "注册" }).closest("form")!,
  );
  expect((await screen.findByRole("alert")).textContent).toContain(
    "两次输入的密码不一致",
  );
  expect(post).not.toHaveBeenCalled();
});

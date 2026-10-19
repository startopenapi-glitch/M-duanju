import { afterEach, expect, test, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import PartnerInvite from "./PartnerInvite";
import { api } from "../lib/api";

const auth = { user: null as null | { email: string }, setUser: vi.fn() };
vi.mock("../App", () => ({ useAuth: () => auth }));
vi.mock("../lib/format", () => ({ toast: vi.fn() }));
afterEach(() => {
  vi.restoreAllMocks();
  auth.user = null;
  auth.setUser.mockReset();
});

test("invite shows the invited mailbox and routes unauthenticated users through login", async () => {
  vi.spyOn(api, "get").mockResolvedValue({
    valid: true,
    status: "pending",
    email: "invitee@example.com",
    name: "合作一号",
    expiresAt: "2026-10-25T00:00:00Z",
  });
  render(
    <MemoryRouter initialEntries={["/partner/invite?token=tok"]}>
      <PartnerInvite />
    </MemoryRouter>,
  );
  expect(await screen.findByText("invitee@example.com")).toBeTruthy();
  const login = screen.getByRole("link", { name: "登录后接受" }) as HTMLAnchorElement;
  expect(login.getAttribute("href")).toContain("next=");
  expect(login.getAttribute("href")).toContain("email=invitee%40example.com");
  expect(screen.queryByRole("button", { name: "接受邀请并开通" })).toBeNull();
});

test("matching mailbox accepts; a different mailbox is refused in the UI", async () => {
  vi.spyOn(api, "get").mockResolvedValue({
    valid: true,
    status: "pending",
    email: "invitee@example.com",
    name: "合作一号",
    expiresAt: "2026-10-25T00:00:00Z",
  });
  const post = vi.spyOn(api, "post").mockResolvedValue({ id: "partner-1" });
  auth.user = { email: "other@example.com" };
  const { rerender } = render(
    <MemoryRouter initialEntries={["/partner/invite?token=tok"]}>
      <PartnerInvite />
    </MemoryRouter>,
  );
  expect(await screen.findByText(/与邀请邮箱不一致/)).toBeTruthy();
  expect(post).not.toHaveBeenCalled();
  auth.user = { email: "INVITEE@example.com" };
  rerender(
    <MemoryRouter initialEntries={["/partner/invite?token=tok"]}>
      <PartnerInvite />
    </MemoryRouter>,
  );
  const accept = await screen.findByRole("button", { name: "接受邀请并开通" });
  await userEvent.click(accept);
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith("/partner/invitations/accept", {
      token: "tok",
    }),
  );
  expect(auth.setUser).not.toHaveBeenCalled();
});

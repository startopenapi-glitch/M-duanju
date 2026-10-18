import { test, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { useState } from "react";
import Modal from "../Modal";
import { Field } from "./Field";
import { ResponsiveList } from "./ResponsiveList";
test("field_error_is_described", () => {
  render(
    <Field id="idea" label="想法" error="至少五个字">
      <input />
    </Field>,
  );
  const i = screen.getByLabelText("想法");
  expect(i.getAttribute("aria-invalid")).toBe("true");
  expect(
    document.getElementById(i.getAttribute("aria-describedby")!)?.textContent,
  ).toBe("至少五个字");
});
test("modal_restores_focus", async () => {
  function Demo() {
    const [o, set] = useState(false);
    return (
      <>
        <button onClick={() => set(true)}>打开</button>
        <Modal open={o} onClose={() => set(false)}>
          <h2>标题</h2>
          <input aria-label="内容" />
          <button>保存</button>
        </Modal>
      </>
    );
  }
  render(<Demo />);
  const b = screen.getByText("打开");
  b.focus();
  fireEvent.click(b);
  await screen.findByRole("dialog");
  await waitFor(() =>
    expect(document.activeElement).toBe(screen.getByLabelText("内容")),
  );
  fireEvent.keyDown(document, { key: "Escape" });
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(document.activeElement).toBe(b);
});
test("mobile_list_has_one_action_tree", () => {
  vi.stubGlobal("matchMedia", () => ({
    matches: true,
    addEventListener() {},
    removeEventListener() {},
  }));
  render(
    <ResponsiveList
      items={[{ id: "a" }]}
      getKey={(x) => x.id}
      columns={[
        { id: "action", header: "操作", cell: () => <button>查看</button> },
      ]}
      renderMobile={() => <button>查看</button>}
      empty="空"
    />,
  );
  expect(screen.getAllByRole("button", { name: "查看" })).toHaveLength(1);
  expect(screen.queryByRole("table")).toBeNull();
  vi.unstubAllGlobals();
});

test("modal summary keeps natural backward focus order", async () => {
  const user = (await import("@testing-library/user-event")).default.setup();
  render(
    <Modal open onClose={() => {}}>
      <input aria-label="idea" />
      <details>
        <summary>Options</summary>
        <input aria-label="advanced" />
      </details>
      <button>Submit</button>
    </Modal>,
  );
  screen.getByText("Options").focus();
  await user.tab({ shift: true });
  expect(document.activeElement).toBe(screen.getByLabelText("idea"));
});

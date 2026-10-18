import {
  CreationProvider,
  useCreationSession,
} from "../features/creation/CreationProvider";
import { ProjectsProvider } from "../features/creation/ProjectsProvider";
import { useState } from "react";
import { Outlet, useNavigate, useLocation } from "react-router-dom";
import { ProjectRail } from "./ProjectRail";
import { NewProjectDialog } from "./NewProjectDialog";
import { NewProjectContext } from "./new-project";
import { useDrawer } from "./useDrawer";
import { useAuth } from "../App";
import { clearToken } from "../lib/api";
import "./layout.css";
import { PageContainer } from "./PageContainer";
import { WorkspaceNav, ADMIN_NAV } from "../features/workspace/WorkspaceNav";
import "../features/workspace/workspace.css";

function CreationRecoveryBanner({
  hidden,
  onOpen,
}: {
  hidden: boolean;
  onOpen: () => void;
}) {
  const { intent } = useCreationSession();
  if (
    hidden ||
    !["creation-unknown", "start-unknown"].includes(intent.phase)
  )
    return null;
  return (
    <div className="note note-info" role="status" aria-live="polite">
      <b>有一次创建/提交等待核对</b>
      <p>{intent.error || "不要重复提交；先核对原请求。"}</p>
      <button className="btn btn-sm" onClick={onOpen}>
        继续核对原提交
      </button>
    </div>
  );
}

export function AppShell() {
  const { user } = useAuth();
  const { pathname } = useLocation();
  const mode = pathname.startsWith("/admin/")
    ? "admin"
    : pathname.startsWith("/partner")
      ? "partner"
      : null;
  const title =
    mode === "admin"
      ? ADMIN_NAV.find((n) => n.to === pathname)?.label || "运营管理"
      : "推广合作";
  const { open, mobile, ref, openDrawer, closeDrawer } = useDrawer();
  const nav = useNavigate();
  const [newOpen, setNewOpen] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);

  return (
    <ProjectsProvider>
      <CreationProvider>
        <NewProjectContext.Provider value={() => setNewOpen(true)}>
          <div className={"app-shell" + (mode ? " ws-shell" : "")}>
            <header className="app-topbar">
              <button
                className="app-menu-btn"
                onClick={openDrawer}
                aria-label="打开菜单"
                aria-expanded={open}
              >
                ☰
              </button>
              <span className="app-topbar-title">
                M-duanju{mode ? ` / ${title}` : ""}
              </span>
            </header>

            <div
              ref={ref}
              role={mobile && open ? "dialog" : undefined}
              aria-modal={mobile && open ? true : undefined}
              aria-label={mobile && open ? "导航菜单" : undefined}
              className={"app-sidebar-wrap" + (open ? " is-open" : "")}
            >
              {mobile && open && (
                <button className="drawer-close btn" onClick={closeDrawer}>
                  关闭菜单
                </button>
              )}
              {mode ? (
                <WorkspaceNav mode={mode} onNavigate={closeDrawer} />
              ) : (
                <ProjectRail
                  onNavigate={closeDrawer}
                  reloadKey={reloadKey}
                  onNew={() => {
                    setNewOpen(true);
                    closeDrawer();
                  }}
                />
              )}
            </div>

            {open && (
              <div
                className="app-backdrop"
                onClick={closeDrawer}
                aria-hidden="true"
              />
            )}

            <main className="app-workspace">
              <PageContainer
                width={
                  pathname.startsWith("/project/")
                    ? "wide"
                    : pathname.startsWith("/settings") ||
                        pathname === "/security"
                      ? "form"
                      : "default"
                }
              >
                <CreationRecoveryBanner
                  hidden={newOpen}
                  onOpen={() => setNewOpen(true)}
                />
                <div key={`${user?.id}:${pathname}`}>
                  <Outlet />
                </div>
              </PageContainer>
            </main>

            {newOpen && (
              <NewProjectDialog
                onClose={() => setNewOpen(false)}
                onCreated={() => {
                  setNewOpen(false);
                  setReloadKey((k) => k + 1);
                }}
              />
            )}
          </div>
        </NewProjectContext.Provider>
      </CreationProvider>
    </ProjectsProvider>
  );
}

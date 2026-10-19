import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import {
  getToken,
  useSessionScope,
  api,
  SESSION_EVENT,
  staleError,
  errorMessage,
  type User,
} from "./lib/api";
import {
  useState,
  useEffect,
  useRef,
  createContext,
  useContext,
  lazy,
  Suspense,
} from "react";
import { AppShell } from "./layout/AppShell";
import ErrorBoundary from "./components/ErrorBoundary";
import { RecoveryNotice, useRecovery } from "./lib/recovery";
import Home from "./pages/Home";
const Login = lazy(() => import("./pages/Login"));
const Register = lazy(() => import("./pages/Register"));
const PartnerLanding = lazy(() => import("./pages/PartnerLanding"));
const Partner = lazy(() => import("./features/partner/PartnerPages"));
const PartnerInvite = lazy(() => import("./pages/PartnerInvite"));
const AdminPartners = lazy(() => import("./pages/AdminPartners"));
const Project = lazy(() => import("./features/creation/ProjectStagePage"));
const Credits = lazy(() => import("./pages/Credits"));
const Settings = lazy(() => import("./pages/Settings"));
const AdminOrders = lazy(() => import("./pages/AdminOrders"));
const AdminUsers = lazy(() => import("./pages/AdminUsers"));
const AdminStats = lazy(() => import("./pages/AdminStats"));

const AdminJobs = lazy(() => import("./pages/AdminJobs"));
const AdminSystem = lazy(() => import("./pages/AdminSystem"));
import { WorkspaceIdentity } from "./features/workspace/identity";

const Ctx = createContext<{
  user: User | null;
  setUser: (u: User | null) => void;
}>({ user: null, setUser: () => {} });
export const useAuth = () => useContext(Ctx);

/** 需要登录 + 管理员权限的守卫 */
function RequireAdmin({ children }: { children: JSX.Element }) {
  const { user } = useAuth();
  if (!user) return <Navigate to="/login" replace />;
  if (user.role !== "admin") {
    return <div className="card note note-err">需要管理员权限</div>;
  }
  return children;
}

export default function App() {
  const sessionScope = useSessionScope();
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(!!getToken());

  const [authError, setAuthError] = useState(""),
    [attempt, setAttempt] = useState(0),
    [token, setCurrentToken] = useState(getToken());
  const recovery = useRecovery(),
    content = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const change = () => {
      setUser(null);
      setCurrentToken(getToken());
      setAuthError("");
    };
    window.addEventListener(SESSION_EVENT, change);
    const storage = (e: StorageEvent) => {
      if (
        e.key === null ||
        e.key === "drama_token" ||
        e.key === "drama_session_revision"
      )
        change();
    };
    window.addEventListener("storage", storage);
    return () => {
      window.removeEventListener(SESSION_EVENT, change);
      window.removeEventListener("storage", storage);
    };
  }, []);
  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    if (!token) {
      setLoading(false);
      setUser(null);
      return;
    }
    setLoading(true);
    setAuthError("");
    api
      .get<User>("/auth/me", controller.signal)
      .then((u) => {
        if (active) setUser(u);
      })
      .catch((e) => {
        if (active && !staleError(e) && getToken())
          setAuthError(errorMessage(e));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      controller.abort();
    };
  }, [token, attempt, sessionScope]);
  useEffect(() => {
    if (content.current) content.current.inert = !!recovery;
  }, [recovery, loading, authError]);

  if (loading)
    return (
      <div className="flex items-center justify-center min-h-screen text-ink3">
        载入中…
      </div>
    );

  if (authError)
    return (
      <div role="alert" className="card">
        暂时无法连接服务器，你的登录信息已保留。<p>{authError}</p>
        <button className="btn" onClick={() => setAttempt((n) => n + 1)}>
          重试
        </button>
      </div>
    );
  return (
    <Ctx.Provider value={{ user, setUser }}>
      <div ref={content}>
        <BrowserRouter>
          <Suspense
            fallback={
              <div className="ws-state" role="status">
                正在加载工作台…
              </div>
            }
          >
            <Routes>
              <Route
                path="/login"
                element={user ? <Navigate to="/" replace /> : <Login />}
              />
              <Route
                path="/register"
                element={user ? <Navigate to="/" replace /> : <Register />}
              />
              <Route path="/p/:code" element={<PartnerLanding />} />
              <Route path="/partner/invite" element={<PartnerInvite />} />
              <Route
                element={
                  <ErrorBoundary>
                    <WorkspaceIdentity key={sessionScope}>
                      <AppShell />
                    </WorkspaceIdentity>
                  </ErrorBoundary>
                }
              >
                <Route
                  path="/"
                  element={user ? <Home /> : <Navigate to="/login" replace />}
                />
                <Route
                  path="/project/:id"
                  element={
                    user ? <Project /> : <Navigate to="/login" replace />
                  }
                />
                <Route
                  path="/project/:id/:stage"
                  element={
                    user ? <Project /> : <Navigate to="/login" replace />
                  }
                />
                <Route
                  path="/credits"
                  element={
                    user ? <Credits /> : <Navigate to="/login" replace />
                  }
                />
                <Route
                  path="/security"
                  element={
                    user ? (
                      <Navigate to="/settings?tab=account" replace />
                    ) : (
                      <Navigate to="/login" replace />
                    )
                  }
                />
                <Route
                  path="/settings"
                  element={
                    user ? <Settings /> : <Navigate to="/login" replace />
                  }
                />
                <Route
                  path="/partner"
                  element={
                    user ? <Partner /> : <Navigate to="/login" replace />
                  }
                />
                <Route
                  path="/partner/customers"
                  element={
                    user ? (
                      <Partner view="customers" />
                    ) : (
                      <Navigate to="/login" replace />
                    )
                  }
                />
                <Route
                  path="/partner/orders"
                  element={
                    user ? (
                      <Partner view="orders" />
                    ) : (
                      <Navigate to="/login" replace />
                    )
                  }
                />
                <Route
                  path="/partner/settings"
                  element={
                    user ? (
                      <Partner view="settings" />
                    ) : (
                      <Navigate to="/login" replace />
                    )
                  }
                />
                <Route
                  path="/admin/stats"
                  element={
                    <RequireAdmin>
                      <AdminStats />
                    </RequireAdmin>
                  }
                />
                <Route
                  path="/admin/users"
                  element={
                    <RequireAdmin>
                      <AdminUsers />
                    </RequireAdmin>
                  }
                />
                <Route
                  path="/admin/orders"
                  element={
                    <RequireAdmin>
                      <AdminOrders />
                    </RequireAdmin>
                  }
                />
                <Route
                  path="/admin/partners"
                  element={
                    <RequireAdmin>
                      <AdminPartners />
                    </RequireAdmin>
                  }
                />
                <Route
                  path="/admin/jobs"
                  element={
                    <RequireAdmin>
                      <AdminJobs />
                    </RequireAdmin>
                  }
                />
                <Route
                  path="/admin/system"
                  element={
                    <RequireAdmin>
                      <AdminSystem />
                    </RequireAdmin>
                  }
                />
              </Route>
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </Suspense>
        </BrowserRouter>
      </div>
      <RecoveryNotice />
    </Ctx.Provider>
  );
}

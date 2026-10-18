import { useState, useEffect, useRef } from "react";
import { PageHeader } from "../components/ui/PageHeader";
import { Field } from "../components/ui/Field";
import { Feedback } from "../components/ui/Feedback";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api, setToken, type User } from "../lib/api";
import { useAuth } from "../App";
import { toast } from "../lib/format";

function safeNext(raw: string | null): string {
  if (!raw || !raw.startsWith("/") || raw.startsWith("//")) return "/";
  return raw;
}

export default function Register() {
  const [params] = useSearchParams();
  const next = safeNext(params.get("next"));
  const [email, setEmail] = useState(params.get("email") || "");
  const [password, setPassword] = useState("");
  const [password2, setPassword2] = useState("");
  const [nickname, setNickname] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(""),
    [notice, setNotice] = useState("");
  const mounted = useRef(true),
    pending = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const reportError = (message: string) => {
    if (mounted.current) setError(message);
  };
  const { setUser } = useAuth();
  const nav = useNavigate();

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (password !== password2) return reportError("两次输入的密码不一致");
    if (password.length < 6) return reportError("密码至少 6 位");
    if (pending.current) return;
    pending.current = true;
    setError("");
    setNotice("");
    setBusy(true);
    try {
      const r = await api.post<{ token: string; user: User }>(
        "/auth/register",
        { email, password, nickname },
      );
      if (!mounted.current) return;
      setToken(r.token);
      setUser(r.user);
      toast("注册成功，欢迎加入", "ok");
      nav(next, { replace: true });
    } catch (err: any) {
      reportError(err.message);
    } finally {
      pending.current = false;
      if (mounted.current) setBusy(false);
    }
  };

  return (
    <div className="auth-page">
      <div className="card w-full max-w-md">
        <PageHeader
          title="注册"
          description="创建账号，开始生成你的第一部短片"
        />
        <Feedback error={error} notice={notice} />
        <form onSubmit={submit} className="space-y-4">
          <Field id="auth-nickname" label="昵称（选填）">
            <input
              id="auth-nickname"
              autoComplete="nickname"
              disabled={busy}
              className="input"
              value={nickname}
              onChange={(e) => setNickname(e.target.value)}
              placeholder="怎么称呼你"
              maxLength={64}
            />
          </Field>
          <Field id="auth-email" label="邮箱">
            <input
              id="auth-email"
              autoComplete="username"
              disabled={busy}
              className="input"
              type="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="you@example.com"
            />
          </Field>
          <Field id="register-password" label="密码（至少 6 位）">
            <input
              id="register-password"
              autoComplete="new-password"
              disabled={busy}
              className="input"
              type="password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
          <Field id="register-repeat" label="确认密码">
            <input
              id="register-repeat"
              autoComplete="new-password"
              disabled={busy}
              className="input"
              type="password"
              required
              value={password2}
              onChange={(e) => setPassword2(e.target.value)}
            />
          </Field>
          <button className="btn btn-primary btn-block" disabled={busy}>
            {busy ? (
              <>
                <span className="spin" /> 注册中…
              </>
            ) : (
              "注册"
            )}
          </button>
        </form>
        <p className="text-sm text-ink3 mt-5 text-center">
          已有账号？
          <Link to="/login" className="text-amberdeep font-semibold">
            登录
          </Link>
        </p>
      </div>
    </div>
  );
}

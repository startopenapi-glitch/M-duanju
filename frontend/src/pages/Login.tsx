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

export default function Login() {
  const [params] = useSearchParams();
  const next = safeNext(params.get("next"));
  const [mode, setMode] = useState<"password" | "code">("password");
  const [email, setEmail] = useState(params.get("email") || "");
  const [password, setPassword] = useState("");
  const [totpCode, setTotpCode] = useState("");
  const [needTotp, setNeedTotp] = useState(false);
  const [secondFactorKind, setSecondFactorKind] = useState<"totp" | "recovery">(
    "totp",
  );
  const [code, setCode] = useState("");
  const [countdown, setCountdown] = useState(0);
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

  useEffect(() => {
    if (!countdown) return;
    const timer = setTimeout(() => setCountdown((n) => n - 1), 1000);
    return () => clearTimeout(timer);
  }, [countdown]);

  // 发送邮箱验证码（60 秒冷却与后端一致）
  const sendCode = async () => {
    if (!email) {
      reportError("请先填写邮箱");
      return;
    }
    if (pending.current) return;
    pending.current = true;
    setError("");
    setNotice("");
    setBusy(true);
    try {
      await api.post("/auth/email-code", { email });
      if (!mounted.current) return;
      setNotice("验证码已发送，10 分钟内有效");
      setCountdown(60);
    } catch (err: any) {
      reportError(err.message);
    } finally {
      pending.current = false;
      if (mounted.current) setBusy(false);
    }
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (pending.current) return;
    pending.current = true;
    setError("");
    setNotice("");
    setBusy(true);
    try {
      const r =
        mode === "password"
          ? await api.post<{ token: string; user: User }>("/auth/login", {
              email,
              password,
              totpCode: totpCode || undefined,
            })
          : await api.post<{ token: string; user: User }>(
              "/auth/login-by-code",
              { email, code, totpCode: totpCode || undefined },
            );
      if (!mounted.current) return;
      setToken(r.token);
      setUser(r.user);
      toast(`欢迎回来，${r.user.nickname}`, "ok");
      nav(next, { replace: true });
    } catch (err: any) {
      // 两种登录入口都遵守后端 needTotp 契约。
      if (err.needTotp === true) {
        setNeedTotp(true);
      }
      reportError(err.message);
    } finally {
      pending.current = false;
      if (mounted.current) setBusy(false);
    }
  };

  return (
    <div className="auth-page">
      <div className="card w-full max-w-md">
        <PageHeader title="登录" description="登录后开始创作你的短片" />
        <Feedback error={error} notice={notice} />

        {/* 登录方式切换 */}
        <div className="flex gap-1 p-1 mb-5 rounded-xl bg-ink/5">
          {(["password", "code"] as const).map((m) => (
            <button
              key={m}
              type="button"
              disabled={busy}
              aria-pressed={mode === m}
              onClick={() => {
                setMode(m);
                setNeedTotp(false);
                setTotpCode("");
              }}
              className={
                "flex-1 py-2 text-sm rounded-lg transition " +
                (mode === m ? "bg-white font-semibold shadow-sm" : "text-ink3")
              }
            >
              {m === "password" ? "密码登录" : "邮箱验证码登录"}
            </button>
          ))}
        </div>

        <form onSubmit={submit} className="space-y-4">
          <Field id="auth-email" label="邮箱">
            <input
              id="auth-email"
              autoComplete="username"
              disabled={busy}
              className="input"
              type="email"
              required
              value={email}
              onChange={(e) => {
                setEmail(e.target.value);
                setNeedTotp(false);
                setTotpCode("");
              }}
              placeholder="you@example.com"
            />
          </Field>

          {mode === "password" && (
            <Field id="auth-password" label="密码">
              <input
                id="auth-password"
                autoComplete="current-password"
                disabled={busy}
                className="input"
                type="password"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
              />
            </Field>
          )}

          {mode === "code" && (
            <div>
              <label className="label" htmlFor="email-code">
                邮箱验证码
              </label>
              <div className="flex gap-2">
                <input
                  id="email-code"
                  autoComplete="one-time-code"
                  inputMode="numeric"
                  disabled={busy}
                  className="input font-mono text-lg tracking-widest flex-1 min-w-0"
                  maxLength={6}
                  value={code}
                  onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
                  placeholder="000000"
                />
                <button
                  type="button"
                  onClick={sendCode}
                  disabled={busy || countdown > 0}
                  className="btn btn-sm whitespace-nowrap"
                >
                  {countdown > 0 ? `${countdown}s` : "获取验证码"}
                </button>
              </div>
              <p className="text-[11px] text-ink3 mt-1">
                验证码 10 分钟内有效，仅可使用一次
              </p>
            </div>
          )}

          {needTotp && (
            <div className="space-y-2">
              <div
                className="flex gap-3 text-sm"
                role="group"
                aria-label="双因素验证方式"
              >
                <button
                  type="button"
                  aria-pressed={secondFactorKind === "totp"}
                  onClick={() => {
                    setSecondFactorKind("totp");
                    setTotpCode("");
                  }}
                >
                  验证器验证码
                </button>
                <button
                  type="button"
                  aria-pressed={secondFactorKind === "recovery"}
                  onClick={() => {
                    setSecondFactorKind("recovery");
                    setTotpCode("");
                  }}
                >
                  使用恢复码
                </button>
              </div>
              <SecondFactorInput
                kind={secondFactorKind}
                value={totpCode}
                onChange={setTotpCode}
              />
              <p className="text-[11px] text-ink3">
                {secondFactorKind === "recovery"
                  ? "输入保存的 10 位恢复码，每码只能使用一次"
                  : "输入验证器显示的 6 位数字"}
              </p>
            </div>
          )}

          <button className="btn btn-primary btn-block" disabled={busy}>
            {busy ? (
              <>
                <span className="spin" /> 登录中…
              </>
            ) : (
              "登录"
            )}
          </button>
        </form>
        <p className="text-sm text-ink3 mt-5 text-center">
          还没有账号？
          <Link to="/register" className="text-amberdeep font-semibold">
            注册
          </Link>
        </p>
      </div>
    </div>
  );
}

export function SecondFactorInput({
  kind,
  value,
  onChange,
}: {
  kind: "totp" | "recovery";
  value: string;
  onChange: (value: string) => void;
}) {
  const recovery = kind === "recovery";
  return (
    <div>
      <label className="label" htmlFor="second-factor">
        {recovery ? "恢复码（10 位）" : "双因素验证码（6 位）"}
      </label>
      <input
        id="second-factor"
        className="input font-mono text-lg tracking-widest"
        autoComplete="one-time-code"
        autoCapitalize="characters"
        spellCheck={false}
        required
        inputMode={recovery ? "text" : "numeric"}
        maxLength={recovery ? 10 : 6}
        pattern={recovery ? "[A-Za-z0-9]{10}" : "[0-9]{6}"}
        value={value}
        onChange={(e) =>
          onChange(
            recovery
              ? e.target.value
                  .toUpperCase()
                  .replace(/[^A-Z0-9]/g, "")
                  .slice(0, 10)
              : e.target.value.replace(/\D/g, "").slice(0, 6),
          )
        }
        placeholder={recovery ? "ABCDE234FG" : "000000"}
        autoFocus
      />
    </div>
  );
}

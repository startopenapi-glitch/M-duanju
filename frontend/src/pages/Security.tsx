import { useEffect, useState, useRef } from "react";
import {
  api,
  clearToken,
  holdSessionExpiry,
  errorMessage,
  staleError,
} from "../lib/api";
import { presentRecovery } from "../lib/recovery";
import { PageHeader } from "../components/ui/PageHeader";
import { Feedback } from "../components/ui/Feedback";
import { toast } from "../lib/format";

/** 安全设置：改密码 + 双因素认证 */
export default function Security() {
  const [formError, setFormError] = useState("");
  const reportError = (message: string) => setFormError(message);
  const [totpEnabled, setTotpEnabled] = useState(false);
  const [loading, setLoading] = useState(true);

  // 改密码
  const [oldPwd, setOldPwd] = useState("");
  const [newPwd, setNewPwd] = useState("");
  const [newPwd2, setNewPwd2] = useState("");
  const [pwdBusy, setPwdBusy] = useState(false);

  // TOTP
  const [setup, setSetup] = useState<{
    secret: string;
    uri: string;
    qrDataUrl: string;
  } | null>(null);
  const [code, setCode] = useState("");
  const [loadError, setLoadError] = useState("");
  const [loadAttempt, setLoadAttempt] = useState(0);
  const alive = useRef(true);
  const [totpBusy, setTotpBusy] = useState(false);
  const [disablePwd, setDisablePwd] = useState("");

  useEffect(() => {
    alive.current = true;
    const controller = new AbortController();
    setLoading(true);
    setLoadError("");
    api
      .get<{ enabled: boolean }>("/auth/totp", controller.signal)
      .then((r) => {
        if (alive.current) setTotpEnabled(r.enabled);
      })
      .catch((e) => {
        if (alive.current && !staleError(e)) setLoadError(errorMessage(e));
      })
      .finally(() => {
        if (alive.current) setLoading(false);
      });
    return () => {
      alive.current = false;
      controller.abort();
    };
  }, [loadAttempt]);
  const relogin = (message: string) => {
    toast(message + "，请重新登录", "ok");
    clearToken();
  };
  const changePwd = async (e: React.FormEvent) => {
    e.preventDefault();
    if (newPwd.length < 8) return reportError("新密码至少 8 位");
    if (newPwd !== newPwd2) return reportError("两次输入的新密码不一致");
    setFormError("");
    setPwdBusy(true);
    try {
      await api.post("/auth/password", {
        oldPassword: oldPwd,
        newPassword: newPwd,
      });
      relogin("密码已修改，旧会话已失效");
      setOldPwd("");
      setNewPwd("");
      setNewPwd2("");
    } catch (e: unknown) {
      if (!staleError(e)) reportError(errorMessage(e));
    } finally {
      setPwdBusy(false);
    }
  };

  const startTotp = async () => {
    setFormError("");
    setTotpBusy(true);
    try {
      const r = await api.post<{
        secret: string;
        uri: string;
        qrDataUrl: string;
      }>("/auth/totp/setup");
      setSetup(r);
    } catch (e: unknown) {
      if (!staleError(e)) reportError(errorMessage(e));
    } finally {
      setTotpBusy(false);
    }
  };

  const enableTotp = async () => {
    if (code.length !== 6) return reportError("请输入 6 位验证码");
    setFormError("");
    setTotpBusy(true);
    const release = holdSessionExpiry();
    try {
      const r = await api.post<{ recoveryCodes: string[] }>(
        "/auth/totp/enable",
        { code },
      );
      if (!r.recoveryCodes?.length) {
        release();
        relogin("双因素认证已启用，但恢复码未返回，请联系管理员");
        return;
      }
      presentRecovery(r.recoveryCodes, release);
      if (alive.current) {
        setTotpEnabled(true);
        setSetup(null);
        setCode("");
      }
    } catch (e: unknown) {
      release();
      if (!staleError(e)) reportError(errorMessage(e));
    } finally {
      if (alive.current) setTotpBusy(false);
    }
  };

  const disableTotp = async () => {
    setFormError("");
    setTotpBusy(true);
    try {
      await api.post("/auth/totp/disable", { password: disablePwd });
      setTotpEnabled(false);
      setDisablePwd("");
      relogin("双因素认证已停用，旧会话已失效");
    } catch (e: unknown) {
      if (!staleError(e)) reportError(errorMessage(e));
    } finally {
      setTotpBusy(false);
    }
  };

  if (loading) return <div className="text-ink3 text-sm">载入中…</div>;

  if (loadError)
    return (
      <div role="alert" className="note note-err">
        安全设置加载失败：{loadError}
        <button className="btn" onClick={() => setLoadAttempt((v) => v + 1)}>
          重试
        </button>
      </div>
    );

  return (
    <div className="space-y-8">
      <PageHeader
        title="账户设置"
        description="管理登录密码与双因素认证，变更后旧会话将失效。"
      />
      <Feedback error={formError} />
      {/* 改密码 */}
      <section>
        <h2 className="text-lg font-bold mb-4">修改密码</h2>
        <form onSubmit={changePwd} className="card space-y-4">
          <div>
            <label htmlFor="old-password" className="label">
              当前密码
            </label>
            <input
              id="old-password"
              className="input"
              type="password"
              required
              value={oldPwd}
              onChange={(e) => setOldPwd(e.target.value)}
              autoComplete="current-password"
            />
          </div>
          <div className="grid md:grid-cols-2 gap-4">
            <div>
              <label htmlFor="new-password" className="label">
                新密码（至少 8 位）
              </label>
              <input
                id="new-password"
                className="input"
                type="password"
                required
                value={newPwd}
                onChange={(e) => setNewPwd(e.target.value)}
                autoComplete="new-password"
              />
            </div>
            <div>
              <label htmlFor="repeat-password" className="label">
                确认新密码
              </label>
              <input
                id="repeat-password"
                className="input"
                type="password"
                required
                value={newPwd2}
                onChange={(e) => setNewPwd2(e.target.value)}
                autoComplete="new-password"
              />
            </div>
          </div>
          <button className="btn btn-primary" disabled={pwdBusy}>
            {pwdBusy ? (
              <>
                <span className="spin" /> 提交中…
              </>
            ) : (
              "修改密码"
            )}
          </button>
        </form>
      </section>

      {/* 双因素 */}
      <section>
        <h2 className="text-lg font-bold mb-4">
          双因素认证
          {totpEnabled && <span className="tag tag-green ml-2">已启用</span>}
        </h2>

        {!totpEnabled && !setup && (
          <div className="card">
            <p className="text-sm text-ink2 mb-3">
              开启后登录需额外输入手机验证器（如 Google
              Authenticator、微信小程序「验证器」）生成的 6 位验证码。
              <b className="text-amberdeep">管理员账号强烈建议开启。</b>
            </p>
            <button
              className="btn btn-primary"
              disabled={totpBusy}
              onClick={startTotp}
            >
              {totpBusy ? (
                <>
                  <span className="spin" /> 生成中…
                </>
              ) : (
                "开启双因素认证"
              )}
            </button>
          </div>
        )}

        {setup && (
          <div className="card space-y-4">
            <div className="note note-info">
              用验证器扫描下方二维码，或手动输入密钥：
              <code className="font-mono">{setup.secret}</code>
            </div>
            <div className="flex justify-center">
              <TotpQRCode dataUrl={setup.qrDataUrl} />
            </div>
            <div>
              <label htmlFor="totp-code" className="label">
                输入验证器上显示的 6 位验证码
              </label>
              <input
                id="totp-code"
                className="input max-w-[200px] font-mono text-lg tracking-widest"
                maxLength={6}
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
                placeholder="000000"
              />
            </div>
            <div className="flex gap-2">
              <button
                className="btn btn-primary"
                disabled={totpBusy}
                onClick={enableTotp}
              >
                {totpBusy ? (
                  <>
                    <span className="spin" /> 校验中…
                  </>
                ) : (
                  "确认启用"
                )}
              </button>
              <button
                className="btn"
                onClick={() => {
                  setSetup(null);
                  setCode("");
                }}
              >
                取消
              </button>
            </div>
          </div>
        )}

        {totpEnabled && (
          <div className="card">
            <p className="text-sm text-ink2 mb-3">
              停用后登录只需密码，安全性下降。曾在旧版扫描二维码的用户，建议停用后重新绑定以更换密钥。
            </p>
            <div className="flex gap-2 items-end flex-wrap">
              <div className="flex-1 min-w-[200px]">
                <label htmlFor="disable-password" className="label">
                  输入密码确认
                </label>
                <input
                  id="disable-password"
                  className="input"
                  type="password"
                  value={disablePwd}
                  onChange={(e) => setDisablePwd(e.target.value)}
                  autoComplete="current-password"
                />
              </div>
              <button
                className="btn btn-danger"
                disabled={totpBusy || !disablePwd}
                onClick={disableTotp}
              >
                停用
              </button>
            </div>
          </div>
        )}
      </section>
    </div>
  );
}

export function TotpQRCode({ dataUrl }: { dataUrl: string }) {
  if (!dataUrl?.startsWith("data:image/png;base64,"))
    return (
      <p className="text-sm text-ink3">
        二维码暂不可用，请使用上方密钥手动绑定。
      </p>
    );
  return (
    <img
      src={dataUrl}
      alt="TOTP 二维码"
      width={200}
      height={200}
      className="border border-line rounded bg-white p-2"
    />
  );
}

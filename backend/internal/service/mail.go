package service

// 邮件通知（SMTP）
//
// 未配置 SMTP 时全部静默跳过，不影响主流程。
// 配置项：
//   SMTP_HOST, SMTP_PORT(默认587), SMTP_USER, SMTP_PASS(_FILE),
//   SMTP_FROM, SMTP_TLS(1=STARTTLS, 默认1)
//
// 发送失败只记日志，绝不阻断业务。

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	htmlsafe "html"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Mailer struct {
	Timeout time.Duration
	Host    string
	Port    int
	User    string
	Pass    string
	From    string
	TLS     bool
	Kind    string // "smtp" | "resend"
	APIKey  string
}

// NewMailer 未配置返回 nil
func NewMailer() *Mailer {
	// Resend（HTTP API）优先：配置了 RESEND_API_KEY / _FILE 就走 Resend，不再需要 SMTP
	key := os.Getenv("RESEND_API_KEY")
	if f := os.Getenv("RESEND_API_KEY_FILE"); f != "" {
		if b, err := os.ReadFile(f); err == nil {
			key = strings.TrimSpace(string(b))
		}
	}
	if key != "" {
		from := os.Getenv("RESEND_FROM")
		if from == "" {
			from = "onboarding@resend.dev"
		}
		return &Mailer{Kind: "resend", APIKey: key, From: from}
	}

	host := os.Getenv("SMTP_HOST")
	from := os.Getenv("SMTP_FROM")
	if host == "" || from == "" {
		return nil
	}
	port := 587
	if p := os.Getenv("SMTP_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}
	pass := os.Getenv("SMTP_PASS")
	if p := os.Getenv("SMTP_PASS_FILE"); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			pass = strings.TrimSpace(string(b))
		}
	}
	return &Mailer{
		Kind: "smtp",
		Host: host, Port: port,
		User: os.Getenv("SMTP_USER"), Pass: pass,
		From: from,
		TLS:  os.Getenv("SMTP_TLS") != "0",
	}
}

func (m *Mailer) Enabled() bool { return m != nil }

// Send 发送纯文本 + 简单 HTML 双段邮件
func (m *Mailer) Send(to, subject, text, html string) error {
	if m == nil {
		return nil
	}
	if strings.ContainsAny(to+m.From, "\r\n") {
		return fmt.Errorf("邮件地址格式无效")
	}
	recipient, e := mail.ParseAddress(to)
	if e != nil {
		return fmt.Errorf("邮件地址格式无效")
	}
	sender, e := mail.ParseAddress(m.From)
	if e != nil {
		return fmt.Errorf("邮件发件地址配置无效")
	}
	if m.Kind == "resend" {
		return m.sendResend(to, subject, text, html)
	}

	var buf bytes.Buffer
	buf.WriteString("From: " + mime.BEncoding.Encode("UTF-8", "短剧工厂") + " <" + m.From + ">\r\n")
	buf.WriteString("To: " + to + "\r\n")
	buf.WriteString("Subject: " + mime.BEncoding.Encode("UTF-8", subject) + "\r\n")
	buf.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	buf.WriteString("MIME-Version: 1.0\r\n")

	if html == "" {
		buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		buf.WriteString(base64.StdEncoding.EncodeToString([]byte(text)))
	} else {
		boundary := "boundary-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		buf.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")

		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		buf.WriteString(base64.StdEncoding.EncodeToString([]byte(text)) + "\r\n")

		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		buf.WriteString(base64.StdEncoding.EncodeToString([]byte(html)) + "\r\n")
		buf.WriteString("--" + boundary + "--\r\n")
	}

	if err := m.sendSMTP(sender.Address, recipient.Address, buf.Bytes()); err != nil {
		return fmt.Errorf("SMTP 发送失败或超时，请稍后重试")
	}
	return nil
}
func (m *Mailer) timeout() time.Duration {
	if m.Timeout > 0 {
		return m.Timeout
	}
	return 15 * time.Second
}
func (m *Mailer) sendSMTP(from, to string, body []byte) error {
	conn, e := net.DialTimeout("tcp", net.JoinHostPort(m.Host, strconv.Itoa(m.Port)), m.timeout())
	if e != nil {
		return e
	}
	defer conn.Close()
	if e = conn.SetDeadline(time.Now().Add(m.timeout())); e != nil {
		return e
	}
	c, e := smtp.NewClient(conn, m.Host)
	if e != nil {
		return e
	}
	defer c.Close()
	if m.TLS {
		if e = c.StartTLS(&tls.Config{ServerName: m.Host, MinVersion: tls.VersionTLS12}); e != nil {
			return e
		}
	}
	if m.User != "" {
		if e = c.Auth(smtp.PlainAuth("", m.User, m.Pass, m.Host)); e != nil {
			return e
		}
	}
	if e = c.Mail(from); e != nil {
		return e
	}
	if e = c.Rcpt(to); e != nil {
		return e
	}
	w, e := c.Data()
	if e != nil {
		return e
	}
	if _, e = w.Write(body); e != nil {
		return e
	}
	if e = w.Close(); e != nil {
		return e
	}
	return c.Quit()
}

// ─────────── 业务邮件 ───────────

// SendWelcome 注册欢迎
// SendLoginCode 发送登录验证码。邮件服务未配置时返回错误，由调用方提示用户。
func (s *Service) SendLoginCode(to, code string) error {
	m := NewMailer()
	if !m.Enabled() {
		return fmt.Errorf("邮件服务未配置")
	}
	text := "你的登录验证码是 " + code + "，10 分钟内有效。如非本人操作请忽略。"
	html := "<p>你的登录验证码是</p><p style=\"font-size:28px;font-weight:700;letter-spacing:4px\">" + code +
		"</p><p>10 分钟内有效。如非本人操作请忽略。</p>"
	return m.Send(to, "登录验证码", text, html)
}

func (s *Service) SendWelcome(to, nickname string) {
	go func() {
		m := NewMailer()
		if !m.Enabled() {
			return
		}
		html := fmt.Sprintf(`<div style="font-family:sans-serif;max-width:520px;margin:0 auto;padding:24px">`+
			`<h2 style="color:#1B2A4A">欢迎加入短剧工厂</h2>`+
			`<p>%s，你的账号已创建成功。</p>`+
			`<p style="color:#6B7A94;font-size:13px">登录后可创建你的第一部 AI 短片。</p>`+
			`<p style="margin-top:24px"><a href="%s" style="background:#1B2A4A;color:#fff;padding:10px 20px;border-radius:4px;text-decoration:none">开始创作</a></p></div>`,
			htmlsafe.EscapeString(nickname), htmlsafe.EscapeString(getenvDefault("PUBLIC_BASE_URL", "")))
		if err := m.Send(to, "欢迎加入短剧工厂", "欢迎加入短剧工厂，你的账号已创建。", html); err != nil {
			log.Printf("[mail] 发送欢迎邮件失败: %v", err)
		}
	}()
}

// SendRechargeSuccess 充值到账
func (s *Service) SendRechargeSuccess(to string, amountYuan float64, credits int64, balance int64) {
	go func() {
		m := NewMailer()
		if !m.Enabled() {
			return
		}
		subject := fmt.Sprintf("充值成功 ¥%.2f", amountYuan)
		text := fmt.Sprintf("充值 ¥%.2f 已到账，增加 %d 积分，当前余额 %d 积分。", amountYuan, credits, balance)
		html := fmt.Sprintf(`<div style="font-family:sans-serif;max-width:520px;margin:0 auto;padding:24px">
<h2 style="color:#1B2A4A">充值成功</h2>
<table style="width:100%%;border-collapse:collapse;font-size:14px">
<tr><td style="padding:8px 0;color:#6B7A94">金额</td><td style="text-align:right;font-weight:600">¥%.2f</td></tr>
<tr><td style="padding:8px 0;color:#6B7A94">积分</td><td style="text-align:right;font-weight:600">+%d</td></tr>
<tr><td style="padding:8px 0;color:#6B7A94;border-top:1px solid #E4DDD1">当前余额</td>
<td style="text-align:right;font-weight:600;border-top:1px solid #E4DDD1">%d 积分</td></tr>
</table></div>`, amountYuan, credits, balance)
		if err := m.Send(to, subject, text, html); err != nil {
			log.Printf("[mail] 发送充值通知失败: %v", err)
		}
	}()
}

// SendPartnerInvite 发送代理邀请链接。邮件是邀请开通的必要链路，未配置/发送失败
// 必须返回给管理员，不能像普通通知一样静默跳过。
func (s *Service) SendPartnerInvite(to, name, token string) error {
	base := strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/")
	if base == "" {
		return errors.New("PUBLIC_BASE_URL 未配置，无法生成邀请链接")
	}
	link := base + "/partner/invite?token=" + url.QueryEscape(token)
	if s.partnerInviteSender != nil {
		return s.partnerInviteSender(to, name, link)
	}
	m := NewMailer()
	if !m.Enabled() {
		return ErrMailUnavailable
	}
	subject := "邀请你开通短剧工厂合作伙伴"
	text := "你被邀请开通短剧工厂合作伙伴（代理）。\n合作方名称：" + name +
		"\n请在 7 天内打开以下链接，登录或注册后接受邀请：\n" + link +
		"\n\n如非本人操作，请忽略本邮件。"
	html := fmt.Sprintf(`<div style="font-family:sans-serif;max-width:560px;margin:0 auto;padding:24px">
<h2 style="color:#1B2A4A">邀请你开通短剧工厂合作伙伴</h2>
<p>你被邀请成为短剧工厂合作伙伴（代理），合作方名称：<b>%s</b>。</p>
<p>请在 7 天内点击下方按钮，登录或注册后接受邀请：</p>
<p style="margin:24px 0"><a href="%s" style="background:#1B2A4A;color:#fff;padding:10px 20px;border-radius:4px;text-decoration:none">接受邀请</a></p>
<p style="color:#6B7A94;font-size:13px">如果按钮无法打开，请复制此链接：%s</p>
<p style="color:#6B7A94;font-size:13px">如非本人操作，请忽略本邮件。</p>
</div>`, htmlsafe.EscapeString(name), htmlsafe.EscapeString(link), htmlsafe.EscapeString(link))
	return m.Send(to, subject, text, html)
}

// SendFinalReady 成片完成
func (s *Service) SendFinalReady(to, title, finalURL string) {
	go func() {
		m := NewMailer()
		if !m.Enabled() {
			return
		}
		html := fmt.Sprintf(`<div style="font-family:sans-serif;max-width:520px;margin:0 auto;padding:24px">
<h2 style="color:#1B2A4A">你的短片《%s》已完成</h2>
<p><a href="%s" style="background:#1B2A4A;color:#fff;padding:10px 20px;border-radius:4px;text-decoration:none">观看成片</a></p>
</div>`, htmlsafe.EscapeString(title), htmlsafe.EscapeString(finalURL))
		if err := m.Send(to, "短片《"+title+"》已完成", "你的短片已完成："+finalURL, html); err != nil {
			log.Printf("[mail] 发送成片通知失败: %v", err)
		}
	}()
}

// sendResend 通过 Resend HTTP API 发信（https://resend.com）
func (m *Mailer) sendResend(to, subject, text, html string) error {
	payload := map[string]interface{}{
		"from": m.From, "to": []string{to}, "subject": subject,
	}
	if html != "" {
		payload["html"] = html
	}
	if text != "" {
		payload["text"] = text
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.APIKey)
	req.Header.Set("Content-Type", "application/json")
	cl := &http.Client{Timeout: m.timeout()}
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("邮件服务连接失败或超时")
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("邮件服务暂不可用（HTTP %d）", resp.StatusCode)
	}
	log.Print("[mail] resend 已发送")
	return nil
}

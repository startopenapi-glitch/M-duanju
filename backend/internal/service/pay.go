package service

// 充值订单业务层
//
// 核心保证：
//  1. 金额一律 int64 分，DB 条件更新保证幂等（回调+查单并发只入账一次）
//  2. credited 二次抢占，防重复交付
//  3. 对账 Worker 每 30 秒扫 pending 订单，主动查单兜底

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"context"
	"database/sql"
	"drama-platform/internal/model"
)

const (
	OrderPending = "pending"
	OrderPaid    = "paid"
	OrderClosed  = "closed"
	OrderExpired = "expired"
	OrderFailed  = "failed"
	OrderReject  = "rejected"
)

// 单 IP 下单频率限制（内存，单实例够用）
var (
	rateMu    sync.Mutex
	rateTable = map[string][]time.Time{}
)

const (
	rateLimit  = 3 // 每分钟最多 3 次
	rateWindow = 60 * time.Second
)

func checkRate(key string) error {
	rateMu.Lock()
	defer rateMu.Unlock()
	now := time.Now()
	list := rateTable[key][:0]
	for _, t := range rateTable[key] {
		if now.Sub(t) < rateWindow {
			list = append(list, t)
		}
	}
	if len(list) >= rateLimit {
		rateTable[key] = list
		return errors.New("操作过于频繁，请稍后再试")
	}
	rateTable[key] = append(list, now)
	return nil
}

// genOutTradeNo 生成 ≤32 位商户单号
func genOutTradeNo() string {
	return fmt.Sprintf("DR%s%s", time.Now().Format("20060102150405"), randSuffix(6))
}

// resolveRechargeRun 校验充值携带的续跑上下文。
// 只有「本人 + 等待充值 + 允许自动续跑 + 项目仍归本人」的执行才能绑定；
// 空 runID 表示普通充值，不猜测启动任务。不能由客户端指定其他账号的执行。
func (s *Service) resolveRechargeRun(userID, runID string) (string, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return "", nil
	}
	if len(runID) > 32 {
		return "", errors.New("续跑任务标识无效")
	}
	var owner, status, projectID string
	var autoResume bool
	err := s.db.QueryRow(`SELECT user_id,status,auto_resume,project_id FROM generation_runs WHERE id=$1`, runID).
		Scan(&owner, &status, &autoResume, &projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("充值关联的任务不存在")
	}
	if err != nil {
		return "", err
	}
	if owner != userID {
		return "", errors.New("无权为该任务充值")
	}
	if status != "waiting_credits" || !autoResume {
		return "", errors.New("该任务当前不需要充值续跑")
	}
	var projectOwner string
	if err := s.db.QueryRow(`SELECT user_id FROM projects WHERE id=$1`, projectID).Scan(&projectOwner); err != nil {
		return "", errors.New("充值关联的任务已失效")
	}
	if projectOwner != userID {
		return "", errors.New("充值关联的任务已失效")
	}
	return runID, nil
}

// CreatePayOrder 创建充值订单
// 幂等：5 分钟内同用户同金额的 pending 订单直接复用
// runID 非空时复用也必须绑定同一执行，不能静默改写原有续跑上下文。
// RechargeTiers 充值赠送档位：充值金额（分）→ 赠送积分。
// 按产品指定：100 元无赠；500 元 7.5 折，到账 66,667（赠 16,667）；
// 3000 元 6.5 折，到账 461,538（赠 161,538）。
// 注意：当前 720P 售价约等于上游成本，赠送会压低毛利；这是明确的促销配置。
var RechargeTiers = map[int64]int64{
	50000:  16667,
	300000: 161538,
}

func rechargeCredits(amountCents int64) int64 {
	credits := amountCents
	if bonus, ok := RechargeTiers[amountCents]; ok {
		credits += bonus
	}
	return credits
}

func (s *Service) CreatePayOrder(userID string, amountCents int64, channel, clientIP, runID string) (*model.RechargeOrder, string, string, bool, error) {
	if amountCents < 100 || amountCents > 1000000 {
		return nil, "", "", false, errors.New("充值金额需在 1-10000 元之间")
	}
	if channel != "native" && channel != "h5" {
		channel = "native"
	}
	runID, err := s.resolveRechargeRun(userID, runID)
	if err != nil {
		return nil, "", "", false, err
	}
	if err := checkRate("pay:" + userID); err != nil {
		return nil, "", "", false, err
	}

	wp := NewWechatPay()
	if !wp.Enabled() {
		// 人工审核模式：仍要落一张待审核订单，否则管理员无单可审、续跑上下文也会丢。
		// 5 分钟内同金额同续跑上下文复用，避免重复点击生成一串待审订单。
		var existing string
		if e := s.db.QueryRow(`SELECT id FROM recharge_orders
			WHERE user_id=$1 AND amount_cents=$2 AND channel='manual' AND status='pending'
			  AND created_at > now() - interval '5 minutes'
			  AND COALESCE(run_id,'') = $3
			ORDER BY created_at DESC LIMIT 1`, userID, amountCents, runID).Scan(&existing); e == nil && existing != "" {
			o, _ := s.GetOrder(existing)
			return o, "", "", true, nil
		}
		credits := rechargeCredits(amountCents)
		tx, e := s.db.Begin()
		if e != nil {
			return nil, "", "", false, e
		}
		defer tx.Rollback()
		var orderID string
		if e = tx.QueryRow(`
			INSERT INTO recharge_orders
			  (user_id, amount_yuan, amount_cents, credits, status, channel, pay_mode, run_id, created_at, updated_at)
			VALUES ($1,$2,$3,$4,'pending','manual','manual',$5,now(),now())
			RETURNING id`,
			userID, float64(amountCents)/100, amountCents, credits, nullIfEmpty(runID)).Scan(&orderID); e != nil {
			return nil, "", "", false, e
		}
		if e = s.partner.SnapshotOrder(context.Background(), tx, orderID, userID); e != nil {
			return nil, "", "", false, e
		}
		if e = tx.Commit(); e != nil {
			return nil, "", "", false, e
		}
		o, _ := s.GetOrder(orderID)
		return o, "", "", true, nil
	}
	if channel == "h5" && !wp.H5Enabled {
		channel = "native"
	}

	// 幂等复用未付订单：续跑上下文必须一致，不能把 A 任务的订单复用给 B 任务。
	var reuseID, reuseCode, reuseH5 string
	err = s.db.QueryRow(`
		SELECT id, COALESCE(code_url,''), COALESCE(h5_url,'') FROM recharge_orders
		WHERE user_id=$1 AND amount_cents=$2 AND channel=$3 AND status='pending'
		  AND created_at > now() - interval '5 minutes'
		  AND COALESCE(code_url,'') <> ''
		  AND COALESCE(run_id,'') = $4
		ORDER BY created_at DESC LIMIT 1`, userID, amountCents, channel, runID).
		Scan(&reuseID, &reuseCode, &reuseH5)
	if err == nil && reuseID != "" {
		o, _ := s.GetOrder(reuseID)
		return o, reuseCode, reuseH5, false, nil
	}

	outTradeNo := genOutTradeNo()
	// 1 分 = 1 积分（1 元 = 100 积分）
	// 充值赠送：只认精确套餐金额，其它金额一律无赠送，防止任意金额套用赠送。
	credits := rechargeCredits(amountCents)
	expireAt := time.Now().Add(30 * time.Minute)

	var orderID string
	// 订单与来源快照必须同事务：复用 pending 订单时保留原快照，不改写归属。
	tx, err := s.db.Begin()
	if err != nil {
		return nil, "", "", false, err
	}
	defer tx.Rollback()
	err = tx.QueryRow(`
		INSERT INTO recharge_orders
		  (user_id, amount_yuan, amount_cents, credits, status, channel, pay_mode,
		   out_trade_no, mock, run_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,'pending',$5,'wechat',$6,$7,$8,now(),now())
		RETURNING id`,
		userID, float64(amountCents)/100, amountCents, credits,
		channel, outTradeNo, wp.Mock, nullIfEmpty(runID)).Scan(&orderID)
	if err != nil {
		return nil, "", "", false, err
	}
	if e := s.partner.SnapshotOrder(context.Background(), tx, orderID, userID); e != nil {
		return nil, "", "", false, e
	}
	if e := tx.Commit(); e != nil {
		return nil, "", "", false, e
	}

	var codeURL, h5URL string
	if channel == "h5" {
		h5URL, err = wp.CreateH5Order(outTradeNo, "M-duanju积分充值", amountCents, clientIP,
			getenvDefault("PUBLIC_BASE_URL", "http://localhost:8088"))
	} else {
		codeURL, err = wp.CreateNativeOrder(outTradeNo, "M-duanju积分充值", amountCents, expireAt)
	}
	if err != nil {
		log.Printf("[pay] 下单失败 user=%s cents=%d ch=%s err=%v", userID, amountCents, channel, err)
		s.db.Exec(`UPDATE recharge_orders SET status='failed', admin_note=$1, updated_at=now() WHERE id=$2`,
			truncate(err.Error(), 180), orderID)
		return nil, "", "", false, fmt.Errorf("微信下单失败：%v", err)
	}

	s.db.Exec(`UPDATE recharge_orders SET code_url=$1, h5_url=$2, updated_at=now() WHERE id=$3`,
		codeURL, h5URL, orderID)

	o, _ := s.GetOrder(orderID)
	return o, codeURL, h5URL, false, nil
}

// GetOrder 查单个订单
func (s *Service) GetOrder(orderID string) (*model.RechargeOrder, error) {
	var o model.RechargeOrder
	err := s.db.QueryRow(`
		SELECT id, user_id, amount_yuan, amount_cents, credits, status,
		       COALESCE(channel,''), COALESCE(pay_mode,'manual'), COALESCE(admin_note,''),
		       COALESCE(out_trade_no,''), COALESCE(code_url,''), COALESCE(h5_url,''),
		       COALESCE(transaction_id,''), credited, mock, created_at,
		       refund_amount_cents, COALESCE(refund_reason,''), refunded_at, COALESCE(refund_no,''),
		       COALESCE(run_id,'')
		FROM recharge_orders WHERE id=$1`, orderID).
		Scan(&o.ID, &o.UserID, &o.AmountYuan, &o.AmountCents, &o.Credits, &o.Status,
			&o.Channel, &o.PayMode, &o.AdminNote, &o.OutTradeNo, &o.CodeURL, &o.H5URL,
			&o.TransactionID, &o.Credited, &o.Mock, &o.CreatedAt,
			&o.RefundAmountCents, &o.RefundReason, &o.RefundedAt, &o.RefundNo, &o.RunID)
	if err != nil {
		return nil, errors.New("订单不存在")
	}
	return &o, nil
}

// OrderOwner 查归属
func (s *Service) OrderOwner(orderID string) (string, error) {
	var uid string
	if err := s.db.QueryRow(`SELECT user_id FROM recharge_orders WHERE id=$1`, orderID).Scan(&uid); err != nil {
		return "", errors.New("订单不存在")
	}
	return uid, nil
}

// ─────────── 入账（幂等核心） ───────────

// settleOrder 核验 + 幂等入账
// 回调与查单可能并发调用，靠 DB 条件更新抢占，只入账一次
func (s *Service) settleOrder(orderID, mchid, appid string, totalCents int64, txid string) error {
	var localCents int64
	var userID, status string
	err := s.db.QueryRow(`SELECT amount_cents, user_id, status FROM recharge_orders WHERE id=$1`, orderID).
		Scan(&localCents, &userID, &status)
	if err != nil {
		return errors.New("订单不存在")
	}

	// ★ 金额核验铁律：差 1 分即拒
	if totalCents != localCents {
		return fmt.Errorf("金额不符: 微信 %d 分 ≠ 本地 %d 分", totalCents, localCents)
	}

	wp := NewWechatPay()
	if wp != nil {
		if wp.MchID != "" && mchid != "" && mchid != wp.MchID {
			return errors.New("mchid 不匹配")
		}
		if wp.AppID != "" && appid != "" && appid != wp.AppID {
			return errors.New("appid 不匹配")
		}
	}

	// ★ paid、credited、余额、流水、唤醒全部在同一事务：
	// 任一失败整体回滚，不会出现「paid 但未入账」或「credited 但无余额」的中间态。
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`
		UPDATE recharge_orders
		SET status='paid', transaction_id=$1, paid_at=now(), handled_at=now(), updated_at=now()
		WHERE id=$2 AND status <> 'paid'`, txid, orderID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// 已 paid：若此前入账失败过（paid 但未 credited），这次继续补入账，
		// 而不是直接返回——否则用户付了钱永远拿不到积分。
		var credited bool
		if e := tx.QueryRow(`SELECT credited FROM recharge_orders WHERE id=$1`, orderID).Scan(&credited); e != nil {
			return e
		}
		if credited {
			return nil // 真正的重复回调，幂等返回
		}
	}
	res2, err := tx.Exec(`
		UPDATE recharge_orders SET credited=true, updated_at=now()
		WHERE id=$1 AND credited=false`, orderID)
	if err != nil {
		return err
	}
	if n, _ := res2.RowsAffected(); n > 0 {
		var credits int64
		if err := tx.QueryRow(`SELECT credits FROM recharge_orders WHERE id=$1`, orderID).Scan(&credits); err != nil {
			return err
		}
		var newBal int64
		if err := tx.QueryRow(`UPDATE users SET credits=credits+$1, updated_at=now()
		                       WHERE id=$2 RETURNING credits`, credits, userID).Scan(&newBal); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO credit_transactions (user_id, amount, balance_after, kind, note)
			VALUES ($1,$2,$3,'recharge',$4)`,
			userID, credits, newBal,
			fmt.Sprintf("微信支付充值 ¥%.2f", float64(credits)/100)); err != nil {
			return err
		}
		// 入账与续跑唤醒同事务：入账提交后即使进程立刻崩溃，唤醒仍可恢复
		var runID sql.NullString
		if e := tx.QueryRow(`SELECT run_id FROM recharge_orders WHERE id=$1`, orderID).Scan(&runID); e != nil {
			return e
		}
		if runID.Valid && runID.String != "" {
			// 只在 run 存在且属于同一用户时才写唤醒；坏 run_id 不得让入账失败。
			var runOwner string
			if e := tx.QueryRow(`SELECT user_id FROM generation_runs WHERE id=$1`, runID.String).Scan(&runOwner); e == nil && runOwner == userID {
				if e := s.runstore.EnqueueInTx(context.Background(), tx, runID.String, orderID, userID); e != nil {
					return e
				}
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		log.Printf("[pay] 入账成功 order=%s user=%s credits=%d", orderID, userID, credits)
		// 异步通知（未配 SMTP 自动跳过）
		var email string
		if s.db.QueryRow(`SELECT email FROM users WHERE id=$1`, userID).Scan(&email) == nil && email != "" {
			s.SendRechargeSuccess(email, float64(credits)/100, credits, newBal)
		}
	}
	return nil
}

// ConfirmOrderByQuery 主动查单确认（前端轮询/手动确认共用）
func (s *Service) ConfirmOrderByQuery(orderID string) (string, error) {
	o, err := s.GetOrder(orderID)
	if err != nil {
		return "", err
	}
	if o.Status == OrderPaid {
		return OrderPaid, nil
	}
	if o.OutTradeNo == "" {
		return o.Status, nil
	}
	wp := NewWechatPay()
	if !wp.Enabled() {
		return o.Status, nil
	}
	q, err := wp.QueryOrder(o.OutTradeNo)
	if err != nil {
		return o.Status, err
	}
	switch q.TradeState {
	case "SUCCESS":
		if err := s.settleOrder(orderID, q.MchID, q.AppID, q.Amount.Total, q.TransactionID); err != nil {
			return o.Status, err
		}
		return OrderPaid, nil
	case "CLOSED":
		s.db.Exec(`UPDATE recharge_orders SET status='closed', updated_at=now() WHERE id=$1 AND status='pending'`, orderID)
		return OrderClosed, nil
	case "PAYERROR":
		s.db.Exec(`UPDATE recharge_orders SET status='failed', updated_at=now() WHERE id=$1 AND status='pending'`, orderID)
		return OrderFailed, nil
	}
	return o.Status, nil
}

// SettleFromNotify 回调入账（验签解密后调用）
func (s *Service) SettleFromNotify(tx *DecryptedTransaction) error {
	var orderID string
	err := s.db.QueryRow(`SELECT id FROM recharge_orders WHERE out_trade_no=$1`, tx.OutTradeNo).Scan(&orderID)
	if err != nil {
		return fmt.Errorf("订单不存在: %s", tx.OutTradeNo)
	}
	return s.settleOrder(orderID, tx.MchID, tx.AppID, tx.Amount.Total, tx.TransactionID)
}

// LogPayNotify 记录回调
func (s *Service) LogPayNotify(outTradeNo, transactionID, eventType, note, raw string) {
	if len(raw) > 4000 {
		raw = raw[:4000]
	}
	s.db.Exec(`
		INSERT INTO pay_notify_logs (out_trade_no, transaction_id, event_type, note, raw_body, handled)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		outTradeNo, nullIfEmpty(transactionID), eventType, note, raw, true)
}

// ─────────── 对账 Worker ───────────

// StartReconcileWorker 每 30 秒扫 pending 订单主动查单
// 这是支付确认的最终保障：即使回调丢失，也能把已付订单补上
func (s *Service) StartReconcileWorker() {
	wp := NewWechatPay()
	if !wp.Enabled() {
		log.Printf("[reconcile] 微信支付未配置，跳过对账 worker")
		return
	}
	log.Printf("[reconcile] 对账 worker 启动（每 30s）")

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			s.reconcileOnce(wp)
		}
	}()
}

func (s *Service) reconcileOnce(wp *WechatPay) {
	// ① 待确认订单
	// 防重窗口 15 秒 < 扫描间隔 30 秒，保证每轮扫描都能覆盖到
	// （原来防重 30 秒会让每个订单变成最快 60 秒才查一次）
	rows, err := s.db.Query(`
		SELECT id, COALESCE(out_trade_no,'') FROM recharge_orders
		WHERE status='pending' AND out_trade_no IS NOT NULL AND out_trade_no <> ''
		  AND created_at < now() - interval '20 seconds'
		  AND updated_at < now() - interval '15 seconds'
		ORDER BY created_at LIMIT 50`)
	if err != nil {
		log.Printf("[reconcile] 查询 pending 订单失败: %v", err)
		return
	}
	type item struct{ id, no string }
	var list []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.id, &it.no) == nil {
			list = append(list, it)
		}
	}
	rows.Close()

	if len(list) > 0 {
		log.Printf("[reconcile] 扫描到 %d 个待确认订单", len(list))
	}

	for _, it := range list {
		q, err := wp.QueryOrder(it.no)
		if err != nil {
			// ★ 以前这里静默 continue，导致问题完全看不到
			log.Printf("[reconcile] 查单失败 order=%s no=%s: %v", it.id, it.no, err)
			continue
		}
		log.Printf("[reconcile] order=%s trade_state=%s", it.id, q.TradeState)
		switch q.TradeState {
		case "SUCCESS":
			if err := s.settleOrder(it.id, q.MchID, q.AppID, q.Amount.Total, q.TransactionID); err != nil {
				log.Printf("[reconcile] 入账失败 %s: %v", it.id, err)
			} else {
				log.Printf("[reconcile] 补单成功 %s txn=%s", it.id, q.TransactionID)
			}
		case "CLOSED":
			s.db.Exec(`UPDATE recharge_orders SET status='closed', updated_at=now() WHERE id=$1 AND status='pending'`, it.id)
		case "PAYERROR":
			s.db.Exec(`UPDATE recharge_orders SET status='failed', updated_at=now() WHERE id=$1 AND status='pending'`, it.id)
		default:
			// NOTPAY / USERPAYING：仅刷新心跳，避免被反复扫
			s.db.Exec(`UPDATE recharge_orders SET updated_at=now() WHERE id=$1`, it.id)
		}
	}

	// ② 超 2 小时仍 pending → 关单 + 标记过期
	expRows, err := s.db.Query(`
		SELECT id, out_trade_no FROM recharge_orders
		WHERE status='pending' AND created_at < now() - interval '2 hours'
		  AND out_trade_no IS NOT NULL AND out_trade_no <> ''
		LIMIT 20`)
	if err == nil {
		var exp []item
		for expRows.Next() {
			var it item
			if expRows.Scan(&it.id, &it.no) == nil {
				exp = append(exp, it)
			}
		}
		expRows.Close()
		for _, it := range exp {
			wp.CloseOrder(it.no)
			s.db.Exec(`UPDATE recharge_orders SET status='expired', updated_at=now() WHERE id=$1 AND status='pending'`, it.id)
		}
	}

	// ③ expired 但 24 小时内：晚到的支付仍要补入账
	lateRows, err := s.db.Query(`
		SELECT id, out_trade_no FROM recharge_orders
		WHERE status='expired' AND created_at > now() - interval '24 hours'
		  AND out_trade_no IS NOT NULL AND out_trade_no <> ''
		LIMIT 20`)
	if err == nil {
		var late []item
		for lateRows.Next() {
			var it item
			if lateRows.Scan(&it.id, &it.no) == nil {
				late = append(late, it)
			}
		}
		lateRows.Close()
		for _, it := range late {
			q, err := wp.QueryOrder(it.no)
			if err != nil || q.TradeState != "SUCCESS" {
				continue
			}
			if err := s.settleOrder(it.id, q.MchID, q.AppID, q.Amount.Total, q.TransactionID); err == nil {
				log.Printf("[reconcile] 晚到支付补单 %s", it.id)
			}
		}
	}
}

// ─────────── 工具 ───────────

const suffixChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randSuffix(n int) string {
	b := make([]byte, n)
	seed := time.Now().UnixNano()
	for i := 0; i < n; i++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		b[i] = suffixChars[int((seed>>33)&0x7FFFFFFF)%len(suffixChars)]
	}
	return string(b)
}

package service

import (
	"encoding/json"
	"errors"
	"net/url"
)

func (w *WechatPay) SubmitRefund(outTradeNo, refundNo string, totalCents, refundCents int64, reason string) (*RefundResponse, error) {
	if w == nil || w.Mock || w.PrivateKey == nil {
		return nil, ErrRefundUnavailable
	}
	raw, e := w.do("POST", "/v3/refund/domestic/refunds", map[string]interface{}{"out_trade_no": outTradeNo, "out_refund_no": refundNo, "reason": truncate(reason, 80), "amount": map[string]interface{}{"refund": refundCents, "total": totalCents, "currency": "CNY"}})
	return decodeRefund(raw, e)
}
func (w *WechatPay) LookupRefund(refundNo string) (*RefundResponse, error) {
	if w == nil || w.Mock || w.PrivateKey == nil {
		return nil, ErrRefundUnavailable
	}
	raw, e := w.do("GET", "/v3/refund/domestic/refunds/"+url.PathEscape(refundNo), nil)
	return decodeRefund(raw, e)
}
func decodeRefund(raw []byte, e error) (*RefundResponse, error) {
	if e != nil {
		return nil, e
	}
	var r RefundResponse
	if e = json.Unmarshal(raw, &r); e != nil {
		return nil, errors.New("invalid refund response")
	}
	return &r, nil
}

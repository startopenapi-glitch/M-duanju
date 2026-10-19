// Package partner 实现单级推广合作伙伴：身份、来源归属、订单来源快照与脱敏统计。
// 一期不含分佣、提现、多级分销；不修改任何价格与积分规则。
package partner

import "errors"

const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
)

// 一期只允许 active / suspended
func ValidStatus(s string) bool { return s == StatusActive || s == StatusSuspended }

var (
	ErrNotFound        = errors.New("合作伙伴不存在")
	ErrAlreadyExists   = errors.New("该账号已是合作伙伴")
	ErrSuspended       = errors.New("合作伙伴已暂停")
	ErrVersionConflict = errors.New("资料已被他人修改，请刷新后重试")
	ErrInvalidCode     = errors.New("推广码无效")
	ErrCodeTaken       = errors.New("推广码已被占用")
)

type Partner struct {
	ID        string `json:"id"`
	UserID    string `json:"userId"`
	Name      string `json:"name"`
	Intro     string `json:"intro"`
	Wechat    string `json:"wechat"`
	Code      string `json:"code"`
	Status    string `json:"status"`
	Version   int64  `json:"version"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// 公开资料：推广页只允许这些字段，绝不包含客户或财务信息
type PublicProfile struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Intro  string `json:"intro"`
	Wechat string `json:"wechat"`
	Usable bool   `json:"usable"`
	Reason string `json:"reason,omitempty"`
}

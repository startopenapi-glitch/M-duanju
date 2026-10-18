package service

import (
	"context"
	"time"

	"drama-platform/internal/partner"
)

// 薄适配：handler 只做 HTTP，不写 SQL；归属与快照规则都在 partner 包里。
func (s *Service) PartnerStore() *partner.Store { return s.partner }

func (s *Service) PartnerPublicProfile(code string) (*partner.PublicProfile, error) {
	return s.partner.PublicProfile(context.Background(), code)
}

// 候选 cookie 用会话同一密钥签名，但带独立用途前缀，不能与会话令牌互用。
func (s *Service) PartnerSignCandidate(code string) string {
	return partner.SignCandidate(s.jwtSecret, code, time.Now(), partner.CandidateTTL)
}

func (s *Service) PartnerVerifyCandidate(raw string) (string, error) {
	return partner.VerifyCandidate(s.jwtSecret, raw, time.Now())
}

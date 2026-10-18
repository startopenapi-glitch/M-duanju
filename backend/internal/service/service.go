package service

import runstore "drama-platform/internal/run"

import "drama-platform/internal/partner"

import "database/sql"

// Service 聚合所有业务依赖。
type Service struct {
	db                  *sql.DB
	partner             *partner.Store
	runstore            *runstore.Store
	job                 *Job
	jwtSecret           string
	loginCodeSender     func(string, string) error
	partnerInviteSender func(string, string, string) error
	refundClient        RefundClient
}
type Option func(*Service)

// WithRefundClient injects only the refund channel transport.
func WithRefundClient(client RefundClient) Option {
	return func(s *Service) { s.refundClient = client }
}

// WithLoginCodeSender replaces only the external mail transport.
func WithLoginCodeSender(sender func(string, string) error) Option {
	return func(s *Service) { s.loginCodeSender = sender }
}

// WithPartnerInviteSender replaces only the partner-invitation mail transport.
func WithPartnerInviteSender(sender func(string, string, string) error) Option {
	return func(s *Service) { s.partnerInviteSender = sender }
}
func New(db *sql.DB, jwtSecret string, options ...Option) *Service {
	s := &Service{db: db, jwtSecret: jwtSecret, partner: partner.New(db), runstore: runstore.New(db)}
	for _, option := range options {
		option(s)
	}
	return s
}

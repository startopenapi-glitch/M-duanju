package service

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type mailTransport func(*http.Request) (*http.Response, error)

func (f mailTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMailerNilAndRedactedProviderErrors(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		defer func() {
			if v := recover(); v != nil {
				t.Fatal("nil mailer panicked")
			}
		}()
		var m *Mailer
		if e := m.Send("person@example.com", "synthetic", "body", ""); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("resend", func(t *testing.T) {
		old := http.DefaultTransport
		http.DefaultTransport = mailTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("sensitive-provider-body synthetic-api-key")), Header: make(http.Header)}, nil
		})
		defer func() { http.DefaultTransport = old }()
		m := &Mailer{Kind: "resend", APIKey: "synthetic-api-key", From: "test@example.com"}
		e := m.Send("person@example.com", "synthetic", "body", "")
		if e == nil || strings.Contains(e.Error(), "sensitive-provider-body") || strings.Contains(e.Error(), "synthetic-api-key") {
			t.Fatal("unsafe provider error", e)
		}
	})
}
func TestSMTPTimeoutAndHeaderValidation(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	release := make(chan struct{})
	defer close(release)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		<-release
	}()
	m := &Mailer{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, From: "test@example.com", Timeout: 100 * time.Millisecond}
	start := time.Now()
	e = m.Send("person@example.com", "synthetic", "body", "")
	if e == nil || time.Since(start) > time.Second {
		t.Fatal("SMTP greeting not bounded", e)
	}
	if e = m.Send("person@example.com\r\nBcc: other@example.com", "synthetic", "body", ""); e == nil {
		t.Fatal("header injection accepted")
	}
}

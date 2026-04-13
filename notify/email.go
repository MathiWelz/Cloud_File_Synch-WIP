// Package notify handles sending the sync report via email using
// SMTP with full TLS/STARTTLS support and no third-party dependencies.
package notify

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"time"

	"cloudsync/config"
)

// Reportable can produce both plain-text and HTML report content.
type Reportable interface {
	Summary() string
	HTMLSummary() string
}

// SendReport sends the sync report to the configured recipient.
func SendReport(cfg config.NotificationConfig, report Reportable) error {
	subject := fmt.Sprintf("☁️ CloudSync Report — %s", time.Now().Format("2006-01-02 15:04"))
	raw := buildMessage(cfg.SMTP.From, cfg.Email, subject, report.HTMLSummary())
	return deliver(cfg.SMTP, cfg.Email, raw)
}

// buildMessage assembles a minimal MIME email with an HTML body.
func buildMessage(from, to, subject, htmlBody string) []byte {
	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\n"+
			"MIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		from, to, subject, htmlBody,
	)
	return []byte(msg)
}

// deliver sends the raw message via SMTP.
// It tries implicit TLS first (port 465); if that fails it falls back to STARTTLS.
func deliver(cfg config.SMTPConfig, to string, msg []byte) error {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	tlsCfg := &tls.Config{
		ServerName: cfg.Host,
		MinVersion: tls.VersionTLS12,
	}

	if cfg.UseTLS {
		// Implicit TLS (port 465)
		conn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("tls dial %s: %w", addr, err)
		}
		defer conn.Close()
		return sendOverConn(conn, auth, cfg.Host, cfg.From, to, msg)
	}

	// STARTTLS (port 587) — connect plain, upgrade with STARTTLS
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	return finishSend(client, auth, cfg.From, to, msg)
}

// sendOverConn wraps an existing (already-TLS) net.Conn in an smtp.Client.
func sendOverConn(conn net.Conn, auth smtp.Auth, host, from, to string, msg []byte) error {
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	return finishSend(client, auth, from, to, msg)
}

func finishSend(client *smtp.Client, auth smtp.Auth, from, to string, msg []byte) error {
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp MAIL: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp RCPT: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

package accounts

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MailSenderFromEnv returns no sender when email delivery is unconfigured.
// File delivery is explicitly restricted to a loopback application origin.
func MailSenderFromEnv(getenv func(string) string, origin string) (func(context.Context, string, string, string) error, error) {
	directory, addr := getenv("MAIL_DIRECTORY"), getenv("SMTP_ADDR")
	if directory != "" && addr != "" {
		return nil, errors.New("choose MAIL_DIRECTORY or SMTP_ADDR")
	}
	if directory != "" {
		u, err := url.Parse(origin)
		if err != nil {
			return nil, errors.New("invalid mail origin")
		}
		host := u.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("file mail requires loopback ALLOWED_ORIGIN")
		}
		if err = os.MkdirAll(directory, 0700); err != nil {
			return nil, err
		}
		if err = os.Chmod(directory, 0700); err != nil {
			return nil, err
		}
		return func(ctx context.Context, to, subject, body string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			id, err := randomID()
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(directory, id+".eml"), []byte("To: "+to+"\nSubject: "+subject+"\n\n"+body), 0600)
		}, nil
	}
	if addr == "" {
		return nil, nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, errors.New("SMTP_ADDR must contain host:port")
	}
	from := getenv("SMTP_FROM")
	parsed, err := mail.ParseAddress(from)
	if err != nil || parsed.Address != from || strings.ContainsAny(from, "\r\n") {
		return nil, errors.New("SMTP_FROM must be a mailbox address")
	}
	username, password := getenv("SMTP_USERNAME"), getenv("SMTP_PASSWORD")
	return func(ctx context.Context, to, subject, body string) error {
		dialer := net.Dialer{Timeout: 10 * time.Second}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		defer conn.Close()
		deadline := time.Now().Add(20 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = conn.SetDeadline(deadline)
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stop()
		client, err := smtp.NewClient(conn, host)
		if err != nil {
			return err
		}
		defer client.Close()
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server requires STARTTLS support")
		}
		if err = client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
		if username != "" {
			if err = client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
				return err
			}
		}
		if err = client.Mail(from); err != nil {
			return err
		}
		if err = client.Rcpt(to); err != nil {
			return err
		}
		writer, err := client.Data()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(writer, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", from, to, mime.QEncoding.Encode("UTF-8", subject), strings.ReplaceAll(body, "\n", "\r\n"))
		if err != nil {
			_ = writer.Close()
			return err
		}
		if err = writer.Close(); err != nil {
			return err
		}
		return client.Quit()
	}, nil
}

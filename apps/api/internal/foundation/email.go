package foundation

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/platform/serviceconfig"
)

func RegisterJobs(mux *asynq.ServeMux, deps platform.Dependencies) {
	mux.HandleFunc("email.report-ready", func(ctx context.Context, task *asynq.Task) error {
		return sendReportReady(ctx, deps, task)
	})
	mux.HandleFunc("email.send", func(ctx context.Context, task *asynq.Task) error {
		var envelope jobs.Envelope
		if err := json.Unmarshal(task.Payload(), &envelope); err != nil {
			return fmt.Errorf("invalid envelope: %w", asynq.SkipRetry)
		}
		var message struct {
			To             string    `json:"to"`
			Subject        string    `json:"subject"`
			Text           string    `json:"text"`
			NotificationID uuid.UUID `json:"notification_id"`
		}
		if err := json.Unmarshal(envelope.Payload, &message); err != nil {
			return fmt.Errorf("invalid email task: %w", asynq.SkipRetry)
		}
		if message.NotificationID != uuid.Nil {
			current, err := notificationEmail(ctx, deps, message.NotificationID)
			if err != nil {
				return err
			}
			if current == nil {
				return nil
			}
			message.To, message.Subject, message.Text = current.To, current.Subject, current.Text
		}
		values, err := serviceconfig.Load(ctx, deps.DB.SQL, "email")
		if err != nil {
			return err
		}
		return sendEmailWithConfig(ctx, values, message.To, message.Subject, message.Text, envelope.EventID.String())
	})
}

func sendEmail(ctx context.Context, to, subject, text, messageID string) error {
	return sendEmailWithConfig(ctx, serviceconfig.Defaults("email"), to, subject, text, messageID)
}
func sendEmailWithConfig(ctx context.Context, values serviceconfig.Values, to, subject, text, messageID string) error {
	if !validEmail(to) || strings.ContainsAny(subject, "\r\n") {
		return fmt.Errorf("invalid email recipient or subject: %w", asynq.SkipRetry)
	}
	host, port, from := values.String("host"), values.String("port"), values.String("from")
	if host == "" {
		return fmt.Errorf("SMTP_HOST is not configured")
	}
	if port == "" {
		port = "587"
	}
	if from == "" {
		from = "my-jira <noreply@myjira.local>"
	}
	sender, err := mail.ParseAddress(from)
	if err != nil {
		return fmt.Errorf("SMTP_FROM is invalid")
	}
	var conn net.Conn
	dialer := net.Dialer{Timeout: 15 * time.Second}
	address := net.JoinHostPort(host, port)
	if values.Bool("secure") {
		conn, err = tls.DialWithDialer(&dialer, "tcp", address, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if !values.Bool("secure") {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err = client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return err
			}
		}
	}
	if username := values.String("user"); username != "" {
		if err = client.Auth(smtp.PlainAuth("", username, values.String("password"), host)); err != nil {
			return err
		}
	}
	if err = client.Mail(sender.Address); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	_, err = io.WriteString(writer, "From: "+sender.String()+"\r\nTo: "+to+"\r\nSubject: "+subject+"\r\nMessage-ID: <"+messageID+"@myjira.local>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n"+text+"\r\n")
	if err != nil {
		writer.Close()
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

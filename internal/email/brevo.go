package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"kumbuild-go/internal/models"
)

type Sender interface {
	Send(ctx context.Context, toEmail, subject, htmlContent string) error
	SendOwnerAlert(ctx context.Context, ownerEmail string, order *models.Order) error
	SendCustomerConfirmation(ctx context.Context, order *models.Order) error
}

type BrevoSender struct {
	apiKey   string
	mailFrom string
	client   *http.Client
}

func NewBrevoSender(apiKey, mailFrom string) *BrevoSender {
	if mailFrom == "" {
		mailFrom = "hello@kumbuild.com"
	}
	return &BrevoSender{
		apiKey:   strings.TrimSpace(apiKey),
		mailFrom: mailFrom,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

type brevoRecipient struct {
	Email string `json:"email"`
}

type brevoSenderInfo struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type brevoPayload struct {
	Sender      brevoSenderInfo  `json:"sender"`
	To          []brevoRecipient `json:"to"`
	Subject     string           `json:"subject"`
	HTMLContent string           `json:"htmlContent"`
}

func (s *BrevoSender) Send(ctx context.Context, toEmail, subject, htmlContent string) error {
	toEmail = strings.TrimSpace(toEmail)
	if toEmail == "" {
		return fmt.Errorf("recipient email is required")
	}

	if s.apiKey == "" {
		log.Printf("[INFO] BREVO_API_KEY empty. Mock send to %s | Subject: %q (length: %d bytes)", toEmail, subject, len(htmlContent))
		return nil
	}

	payload := brevoPayload{
		Sender: brevoSenderInfo{
			Name:  "Kumbuild",
			Email: s.mailFrom,
		},
		To:          []brevoRecipient{{Email: toEmail}},
		Subject:     subject,
		HTMLContent: htmlContent,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal brevo payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.brevo.com/v3/smtp/email", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create brevo request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", s.apiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send brevo request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("brevo api error (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func (s *BrevoSender) SendOwnerAlert(ctx context.Context, ownerEmail string, o *models.Order) error {
	displayName := o.BusinessName
	if displayName == "" {
		displayName = o.Name
	}
	subject := fmt.Sprintf("New order %s — %s", o.Reference, displayName)

	emailStr := "Not provided"
	if o.Email != nil && *o.Email != "" {
		emailStr = *o.Email
	}

	html := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="margin:0;padding:24px;background-color:#f8fafc;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#1e293b;">
  <div style="max-width:600px;margin:0 auto;background:#ffffff;border-radius:12px;border:1px solid #e2e8f0;overflow:hidden;box-shadow:0 4px 6px -1px rgba(0,0,0,0.05);">
    <div style="background:#008efd;padding:24px 32px;color:#ffffff;">
      <h1 style="margin:0;font-size:20px;font-weight:700;letter-spacing:-0.5px;">New Website Order Received</h1>
      <p style="margin:6px 0 0 0;font-size:14px;opacity:0.9;">Reference: <strong style="letter-spacing:1px;">%s</strong></p>
    </div>
    <div style="padding:32px;">
      <table style="width:100%%;border-collapse:collapse;font-size:14px;">
        <tr style="border-bottom:1px solid #f1f5f9;"><td style="padding:10px 0;color:#64748b;width:140px;">Customer Name</td><td style="padding:10px 0;font-weight:600;">%s</td></tr>
        <tr style="border-bottom:1px solid #f1f5f9;"><td style="padding:10px 0;color:#64748b;">Business Name</td><td style="padding:10px 0;font-weight:600;">%s</td></tr>
        <tr style="border-bottom:1px solid #f1f5f9;"><td style="padding:10px 0;color:#64748b;">Business Type</td><td style="padding:10px 0;">%s</td></tr>
        <tr style="border-bottom:1px solid #f1f5f9;"><td style="padding:10px 0;color:#64748b;">Package</td><td style="padding:10px 0;font-weight:600;text-transform:uppercase;color:#008efd;">%s</td></tr>
        <tr style="border-bottom:1px solid #f1f5f9;"><td style="padding:10px 0;color:#64748b;">Phone</td><td style="padding:10px 0;">%s</td></tr>
        <tr style="border-bottom:1px solid #f1f5f9;"><td style="padding:10px 0;color:#64748b;">WhatsApp</td><td style="padding:10px 0;">%s</td></tr>
        <tr style="border-bottom:1px solid #f1f5f9;"><td style="padding:10px 0;color:#64748b;">Email</td><td style="padding:10px 0;">%s</td></tr>
        <tr style="border-bottom:1px solid #f1f5f9;"><td style="padding:10px 0;color:#64748b;">Language</td><td style="padding:10px 0;">%s</td></tr>
        <tr><td style="padding:10px 0;color:#64748b;vertical-align:top;">Notes / Message</td><td style="padding:10px 0;line-height:1.5;">%s</td></tr>
      </table>
    </div>
    <div style="padding:16px 32px;background:#f8fafc;border-top:1px solid #e2e8f0;font-size:12px;color:#94a3b8;text-align:center;">
      Kumbuild Order Management Engine &bull; kumbuild.com
    </div>
  </div>
</body>
</html>`,
		html.EscapeString(o.Reference),
		html.EscapeString(o.Name),
		html.EscapeString(o.BusinessName),
		html.EscapeString(o.BusinessType),
		html.EscapeString(o.Package),
		html.EscapeString(o.Phone),
		html.EscapeString(o.WhatsApp),
		html.EscapeString(emailStr),
		html.EscapeString(o.ContentLanguage),
		html.EscapeString(o.Message),
	)

	return s.Send(ctx, ownerEmail, subject, html)
}

func (s *BrevoSender) SendCustomerConfirmation(ctx context.Context, o *models.Order) error {
	if o.Email == nil || strings.TrimSpace(*o.Email) == "" {
		return nil
	}

	subject := "We received your order — Kumbuild"
	customerEmail := strings.TrimSpace(*o.Email)

	html := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="margin:0;padding:24px;background-color:#f8fafc;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#1e293b;">
  <div style="max-width:560px;margin:0 auto;background:#ffffff;border-radius:12px;border:1px solid #e2e8f0;overflow:hidden;box-shadow:0 4px 6px -1px rgba(0,0,0,0.05);">
    <div style="background:#008efd;padding:28px 32px;color:#ffffff;text-align:center;">
      <h1 style="margin:0;font-size:24px;font-weight:700;letter-spacing:-0.5px;">Order Received!</h1>
      <p style="margin:8px 0 0 0;font-size:15px;opacity:0.95;">Thank you for choosing Kumbuild</p>
    </div>
    <div style="padding:32px;">
      <p style="font-size:15px;line-height:1.6;margin-top:0;">Hi <strong>%s</strong>,</p>
      <p style="font-size:15px;line-height:1.6;color:#475569;">
        We have received your website request for <strong>%s</strong>. Your order reference is:
      </p>
      <div style="background:#f0f9ff;border:1px dashed #008efd;border-radius:8px;padding:14px;text-align:center;margin:24px 0;">
        <span style="font-size:22px;font-weight:700;color:#008efd;letter-spacing:2px;">%s</span>
      </div>
      <h3 style="font-size:16px;color:#0f172a;margin:24px 0 12px 0;">What happens next?</h3>
      <ol style="font-size:14px;line-height:1.7;color:#475569;padding-left:20px;margin:0;">
        <li><strong>WhatsApp Confirmation:</strong> Our team will connect with you directly on WhatsApp to confirm your website details and content requirements.</li>
        <li><strong>Transparent Advance:</strong> 50%% advance payment is only requested <em>after</em> we confirm all your requirements.</li>
        <li><strong>Rapid Delivery:</strong> Your live website preview will be ready in <strong>3–5 working days</strong> for your feedback and revisions.</li>
      </ol>
      <p style="font-size:14px;line-height:1.6;color:#64748b;margin-top:24px;">
        If you have any questions in the meantime, reply directly to this email or reach us on WhatsApp.
      </p>
      <p style="font-size:14px;color:#334155;margin-bottom:0;">
        Warm regards,<br>
        <strong>The Kumbuild Team</strong>
      </p>
    </div>
    <div style="padding:16px 32px;background:#f8fafc;border-top:1px solid #e2e8f0;font-size:12px;color:#94a3b8;text-align:center;">
      Kumbuild &bull; Done-for-you websites worldwide &bull; <a href="https://kumbuild.com" style="color:#008efd;text-decoration:none;">kumbuild.com</a>
    </div>
  </div>
</body>
</html>`,
		html.EscapeString(o.Name),
		html.EscapeString(func() string {
			if o.BusinessName != "" {
				return o.BusinessName
			}
			return "your business"
		}()),
		html.EscapeString(o.Reference),
	)

	return s.Send(ctx, customerEmail, subject, html)
}

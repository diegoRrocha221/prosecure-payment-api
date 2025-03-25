package email

import (
    "crypto/tls"
    "fmt"
    "net"
    "net/smtp"
)

type SMTPConfig struct {
    Host     string
    Port     string
    Username string
    Password string
}

type SMTPService struct {
    config SMTPConfig
}

func NewSMTPService(config SMTPConfig) *SMTPService {
    return &SMTPService{
        config: config,
    }
}

func (s *SMTPService) SendEmail(to, subject, body string) error {
    tlsConfig := &tls.Config{
        InsecureSkipVerify: true,
        ServerName:         s.config.Host,
    }

    conn, err := net.Dial("tcp", fmt.Sprintf("%s:%s", s.config.Host, s.config.Port))
    if err != nil {
        return fmt.Errorf("failed to connect to SMTP server: %v", err)
    }

    client, err := smtp.NewClient(conn, s.config.Host)
    if err != nil {
        return fmt.Errorf("failed to create SMTP client: %v", err)
    }
    defer client.Close()

    if err = client.StartTLS(tlsConfig); err != nil {
        return fmt.Errorf("failed to start TLS: %v", err)
    }


    if err = client.Mail("no-reply@prosecure.com"); err != nil {
        return fmt.Errorf("failed to set sender: %v", err)
    }
    if err = client.Rcpt(to); err != nil {
        return fmt.Errorf("failed to set recipient: %v", err)
    }


    w, err := client.Data()
    if err != nil {
        return fmt.Errorf("failed to create email body writer: %v", err)
    }

    headers := fmt.Sprintf(
        "From: ProSecure <%s>\r\n"+
            "To: %s\r\n"+
            "Subject: %s\r\n"+
            "MIME-Version: 1.0\r\n"+
            "Content-Type: text/html; charset=UTF-8\r\n"+
            "\r\n",
        "no-reply@prosecure.com", to, subject,
    )

    if _, err = w.Write([]byte(headers + body)); err != nil {
        return fmt.Errorf("failed to write email body: %v", err)
    }

    if err = w.Close(); err != nil {
        return fmt.Errorf("failed to close email body writer: %v", err)
    }

    return client.Quit()
}

func (s *SMTPService) SendActivationEmail(to, username, code string) error {
    content := fmt.Sprintf(`
        <h2>Welcome to your plan</h2>
        <p>Please confirm your email address to activate your account.</p>
        <p>Your activation code is: %s</p>
    `, code)
    
    return s.SendEmail(to, "Please Confirm Your Email Address", content)
}

func (s *SMTPService) SendInvoiceEmail(to, name, invoice string) error {
    return s.SendEmail(to, "Your invoice has been delivered :)", invoice)
}
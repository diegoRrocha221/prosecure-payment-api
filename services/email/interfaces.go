package email

type EmailSender interface {
    SendEmail(to, subject, body string) error
    SendActivationEmail(to, username, code string) error
    SendInvoiceEmail(to, name, invoice string) error
}
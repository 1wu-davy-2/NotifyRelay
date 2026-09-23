// Package email delivers notifications over SMTP.
//
// It is the first channel implementation, and doubles as the worked example
// for the channel contract: it declares what it can render, returns the
// mandatory three-class result, and contains no retry, rate limiting, format
// conversion or auditing — the core does all of that around it.
package email

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/wneessen/go-mail"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/message"
)

func init() {
	channel.Register(channel.Descriptor{
		Type:         "email",
		ParamSchema:  paramSchema(),
		Capability:   capability(),
		Factory:      New,
		TargetScheme: "mailto",
		ParseTarget:  parseTarget,
	})
}

// maxRecipients bounds one delivery's recipient list.
//
// It matters more than it looks: Send opens one SMTP session per recipient, so
// a caller who chooses the list chooses how many connections this channel makes
// on one unit of its quota. The number matches what the SMTP inbound path
// already accepts in a single transaction (internal/smtpin), so a message that
// can arrive can also be forwarded.
const maxRecipients = 50

// capability is declared once so the registered descriptor and a live
// instance cannot disagree.
func capability() channel.Capability {
	return channel.Capability{
		// Mail has no meaningful body limit: a receiving server's own limit is
		// orders of magnitude larger than any notification.
		SupportedFormats:  []message.Format{message.FormatText, message.FormatHTML},
		SupportAttachment: false, // M2+
		OverflowMode:      channel.OverflowTruncate,
		MaxRecipients:     maxRecipients,
	}
}

// Channel delivers notifications to its configured recipients, or to the ones
// a caller names.
type Channel struct {
	instance string
	cfg      Config
	// tlsConfig is built once at startup so a bad ca_file fails the service
	// at boot rather than on the first notification.
	tlsConfig *tls.Config
}

// New builds an email channel instance from a configuration block.
func New(instance string, raw map[string]any) (channel.Channel, error) {
	cfg, err := parseConfig(raw)
	if err != nil {
		return nil, err
	}

	ch := &Channel{instance: instance, cfg: cfg}

	if cfg.CAFile != "" {
		pool := x509.NewCertPool()
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("parameter \"ca_file\": %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("parameter \"ca_file\": %q contains no PEM certificates", cfg.CAFile)
		}
		ch.tlsConfig = &tls.Config{
			RootCAs:    pool,
			ServerName: cfg.Host,
			MinVersion: tls.VersionTLS12,
		}
	}

	return ch, nil
}

// Type implements channel.Channel.
func (c *Channel) Type() string { return "email" }

// Capability implements channel.Channel.
//
// NeedsRecipients is per instance: an email channel either has recipients
// configured or it is one that can only be used by a caller naming its own.
func (c *Channel) Capability() channel.Capability {
	capability := capability()
	capability.NeedsRecipients = len(c.cfg.To) == 0
	return capability
}

// ParamSchema implements channel.Channel.
func (c *Channel) ParamSchema() []channel.ParamSpec { return paramSchema() }

// Send delivers the message to every recipient of this delivery.
//
// One SMTP transaction per recipient rather than a single transaction with
// several RCPTs. That costs an extra connection per recipient and buys two
// things worth more here: an exact per-recipient outcome, and not disclosing
// the recipient list to every recipient.
func (c *Channel) Send(ctx context.Context, msg *message.Message, target channel.Target) channel.Result {
	// The caller's list wins outright when there is one. Appending to the
	// configured list instead would mean a fixed recipient — a compliance copy,
	// a shared mailbox — silently receives every password-reset link that was
	// addressed to somebody else.
	addrs := target.Recipients
	if len(addrs) == 0 {
		addrs = c.cfg.To
	}
	if len(addrs) == 0 {
		// Nothing to retry: no attempt at retrying produces an address. The
		// class is PERMANENT so the queue gives up rather than holding the
		// message for the retention window.
		return channel.Permanent(
			fmt.Errorf("channel %q has no recipients configured and the request named none", c.instance),
			"no recipients for this delivery")
	}

	recipients := make([]channel.Recipient, 0, len(addrs))
	for _, to := range addrs {
		res := c.deliver(ctx, msg, to)
		recipients = append(recipients, channel.Recipient{
			Address:  to,
			Accepted: res.Class == channel.ClassSent,
			Class:    res.Class,
			Detail:   res.Detail,
		})
	}
	return channel.FromRecipients(recipients)
}

func (c *Channel) deliver(ctx context.Context, msg *message.Message, to string) channel.Result {
	m, err := buildMsg(c.cfg, msg, to)
	if err != nil {
		// The message could not be assembled; no peer was involved, and no
		// retry will change that.
		return channel.Permanent(err, "message could not be assembled")
	}

	client, err := c.newClient()
	if err != nil {
		return channel.Permanent(err, "client configuration is invalid")
	}

	if err := client.DialAndSendWithContext(ctx, m); err != nil {
		return classify(err)
	}
	return channel.Sent("delivered to " + to)
}

// Test connects and authenticates without sending anything.
func (c *Channel) Test(ctx context.Context) channel.Result {
	client, err := c.newClient()
	if err != nil {
		return channel.Permanent(err, "client configuration is invalid")
	}
	if err := client.DialWithContext(ctx); err != nil {
		return classify(err)
	}
	if err := client.Close(); err != nil {
		return channel.Transient(err, "connected but the session did not close cleanly")
	}
	return channel.Sent("connected to " + c.cfg.Host)
}

func (c *Channel) newClient() (*mail.Client, error) {
	opts := []mail.Option{
		mail.WithPort(c.cfg.Port),
		mail.WithTimeout(c.cfg.Timeout),
	}
	if c.cfg.Helo != "" {
		opts = append(opts, mail.WithHELO(c.cfg.Helo))
	}
	if c.tlsConfig != nil {
		opts = append(opts, mail.WithTLSConfig(c.tlsConfig))
	}

	switch c.cfg.TLS {
	case TLSImplicit:
		opts = append(opts, mail.WithSSL())
	case TLSStartTLS:
		if c.cfg.RequireTLS {
			opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
		} else {
			opts = append(opts, mail.WithTLSPolicy(mail.TLSOpportunistic))
		}
	case TLSNone:
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	}

	if c.cfg.Username != "" {
		opts = append(opts,
			mail.WithSMTPAuth(smtpAuthType(c.cfg.AuthType)),
			mail.WithUsername(c.cfg.Username),
			mail.WithPassword(c.cfg.Password),
		)
	}

	return mail.NewClient(c.cfg.Host, opts...)
}

func smtpAuthType(t AuthType) mail.SMTPAuthType {
	switch t {
	case AuthPlain:
		return mail.SMTPAuthPlain
	case AuthLogin:
		return mail.SMTPAuthLogin
	case AuthCramMD5:
		return mail.SMTPAuthCramMD5
	default:
		return mail.SMTPAuthAutoDiscover
	}
}

package smtpin

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-smtp"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/config"
	"notifyrelay/internal/message"
	"notifyrelay/internal/requestid"
	"notifyrelay/internal/router"
)

const (
	readTimeout     = 30 * time.Second
	writeTimeout    = 30 * time.Second
	maxMessageBytes = 4 << 20 // 4 MiB
	maxRecipients   = 50
)

// Deliverer is what the SMTP layer needs from the core.
type Deliverer interface {
	Deliver(ctx context.Context, requestID, target string, msg *message.Message) router.TargetResult
	Instances() []string
}

// Server accepts notifications over SMTP.
type Server struct {
	cfg       config.SMTPInConfig
	deliverer Deliverer
	log       *slog.Logger
	auth      *authenticator
	allowed   []*net.IPNet

	smtp *smtp.Server
}

// New builds the SMTP inbound server from configuration.
func New(cfg config.SMTPInConfig, deliverer Deliverer, log *slog.Logger) (*Server, error) {
	allowed, err := parseAllowedIPs(cfg.AllowedIPs)
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:       cfg,
		deliverer: deliverer,
		log:       log,
		allowed:   allowed,
	}
	if cfg.Auth.Enabled {
		s.auth = newAuthenticator(cfg.Auth.Username, cfg.Auth.Password)
	}

	srv := smtp.NewServer(s)
	srv.Domain = cfg.Hostname
	srv.Addr = cfg.Addr
	srv.ReadTimeout = readTimeout
	srv.WriteTimeout = writeTimeout
	srv.MaxMessageBytes = maxMessageBytes
	srv.MaxRecipients = maxRecipients
	// Credentials are only ever offered over a TLS connection.
	srv.AllowInsecureAuth = false

	s.smtp = srv
	return s, nil
}

// ListenAndServe blocks until the listener fails or Shutdown is called.
func (s *Server) ListenAndServe() error { return s.smtp.ListenAndServe() }

// Shutdown stops accepting connections and waits for active ones to finish.
func (s *Server) Shutdown(ctx context.Context) error { return s.smtp.Shutdown(ctx) }

// NewSession implements smtp.Backend.
func (s *Server) NewSession(c *smtp.Conn) (smtp.Session, error) {
	if !s.ipAllowed(c.Conn().RemoteAddr()) {
		s.log.Warn("smtp: rejected connection from outside the allow list",
			slog.String("remote", c.Conn().RemoteAddr().String()))
		return nil, &smtp.SMTPError{
			Code:    554,
			Message: "relay access denied",
		}
	}

	base := &session{server: s, conn: c}
	if s.auth != nil {
		return &authSession{session: base}, nil
	}
	return base, nil
}

func (s *Server) ipAllowed(addr net.Addr) bool {
	if len(s.allowed) == 0 {
		// No allow list configured: permit every source. Documented as a
		// development-only setting in configs/notifyrelay.example.yaml.
		return true
	}

	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	for _, network := range s.allowed {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func parseAllowedIPs(entries []string) ([]*net.IPNet, error) {
	nets := make([]*net.IPNet, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if !strings.Contains(entry, "/") {
			ip := net.ParseIP(entry)
			if ip == nil {
				return nil, fmt.Errorf("smtp_in.allowed_ips: %q is not an IP address or CIDR", entry)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			entry = fmt.Sprintf("%s/%d", ip.String(), bits)
		}
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			return nil, fmt.Errorf("smtp_in.allowed_ips: %q is not a valid CIDR: %w", entry, err)
		}
		nets = append(nets, network)
	}
	return nets, nil
}

// session handles one SMTP conversation.
type session struct {
	server *Server
	conn   *smtp.Conn
	from   string
	rcpts  []envelopeRecipient
}

type envelopeRecipient struct {
	address string
	alias   string
	typ     message.Type
}

func (s *session) Reset() {
	s.from = ""
	s.rcpts = nil
}

func (s *session) Logout() error { return nil }

func (s *session) Mail(from string, _ *smtp.MailOptions) error {
	s.from = from
	s.rcpts = nil
	return nil
}

// Rcpt validates the alias immediately, so a typo is rejected at RCPT time
// with a 550 rather than being accepted and then bounced.
func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	alias, typ, err := ParseRecipient(to)
	if err != nil {
		return &smtp.SMTPError{Code: 550, Message: err.Error()}
	}

	for _, known := range s.server.deliverer.Instances() {
		if known == alias {
			s.rcpts = append(s.rcpts, envelopeRecipient{address: to, alias: alias, typ: typ})
			return nil
		}
	}

	return &smtp.SMTPError{
		Code:    550,
		Message: fmt.Sprintf("unknown channel %q (known: %s)", alias, strings.Join(s.server.deliverer.Instances(), ", ")),
	}
}

// Data converts the received message and hands it to the router.
//
// The reply code follows the same three-class contract the channels use: a
// retryable failure is reported as 4xx so the sending MTA retries on its own
// schedule, a permanent failure as 5xx so it bounces instead of looping.
func (s *session) Data(r io.Reader) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return &smtp.SMTPError{Code: 451, Message: "failed to read message body"}
	}

	msg, err := toMessage(raw)
	if err != nil {
		return &smtp.SMTPError{Code: 550, Message: err.Error()}
	}

	requestID := requestid.New()
	s.server.log.Info("smtp: message accepted",
		slog.String("request_id", requestID),
		slog.String("from", s.from),
		slog.Int("recipients", len(s.rcpts)),
	)

	retryable, permanent := false, false
	for _, rcpt := range s.rcpts {
		out := *msg
		out.Type = rcpt.typ

		res := s.server.deliverer.Deliver(context.Background(), requestID, rcpt.alias, &out)
		switch res.Class() {
		case channel.ClassSent:
			// nothing to record here; the router already audited it
		case channel.ClassConnectError, channel.ClassTransient:
			retryable = true
		default:
			permanent = true
		}
	}

	switch {
	case retryable:
		return &smtp.SMTPError{Code: 451, Message: "delivery deferred, try again later"}
	case permanent:
		return &smtp.SMTPError{Code: 550, Message: "delivery permanently failed"}
	default:
		return nil
	}
}

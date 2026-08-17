// Package smtp provides an SMTP sender with explicit transport security.
package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sudosylabs/proctor/packages/mail"
)

// Security controls how the SMTP connection is protected.
type Security string

const (
	// SecurityNone uses cleartext SMTP and forbids authentication.
	SecurityNone Security = "none"
	// SecuritySTARTTLS requires the server to advertise and complete STARTTLS.
	SecuritySTARTTLS Security = "starttls"
	// SecurityTLS establishes implicit TLS before the SMTP greeting.
	SecurityTLS Security = "tls"
)

// Authentication selects an SMTP SASL mechanism.
type Authentication string

const (
	// AuthenticationNone disables authentication.
	AuthenticationNone Authentication = "none"
	// AuthenticationAuto prefers PLAIN and falls back to LOGIN.
	AuthenticationAuto Authentication = "auto"
	// AuthenticationPlain requires the PLAIN mechanism.
	AuthenticationPlain Authentication = "plain"
	// AuthenticationLogin requires the LOGIN mechanism.
	AuthenticationLogin Authentication = "login"
)

// Config controls SMTP connections and message composition.
type Config struct {
	Address         string
	ServerName      string
	LocalName       string
	Security        Security
	TLSConfig       *tls.Config
	Username        string
	Password        string
	Authentication  Authentication
	Timeout         time.Duration
	MessageIDDomain string
	MaxMessageBytes int64
	MaxRecipients   int
}

// Sender opens one SMTP connection per message. It is safe for concurrent use
// and leaves connection pooling or asynchronous queues to higher layers.
type Sender struct {
	address        string
	serverName     string
	localName      string
	security       Security
	tlsConfig      *tls.Config
	username       string
	password       string
	authentication Authentication
	timeout        time.Duration
	composer       *mail.Composer
}

// New validates configuration and constructs an SMTP sender.
func New(config Config) (*Sender, error) {
	host, port, err := net.SplitHostPort(config.Address)
	if err != nil || host == "" {
		return nil, fmt.Errorf("smtp mail: address must be host:port")
	}
	if parsedPort, parseErr := strconv.Atoi(port); parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
		return nil, fmt.Errorf("smtp mail: invalid port")
	}
	if config.ServerName == "" {
		config.ServerName = host
	}
	if config.ServerName == "" || strings.ContainsAny(config.ServerName, "\r\n\x00") {
		return nil, fmt.Errorf("smtp mail: invalid server name")
	}
	if config.LocalName != "" && strings.ContainsAny(config.LocalName, "\r\n\x00") {
		return nil, fmt.Errorf("smtp mail: invalid local name")
	}
	if config.Security == "" {
		config.Security = SecurityNone
	}
	switch config.Security {
	case SecurityNone, SecuritySTARTTLS, SecurityTLS:
	default:
		return nil, fmt.Errorf("smtp mail: unknown security mode %q", config.Security)
	}

	if config.Authentication == "" {
		if config.Username == "" {
			config.Authentication = AuthenticationNone
		} else {
			config.Authentication = AuthenticationAuto
		}
	}
	switch config.Authentication {
	case AuthenticationNone:
		if config.Username != "" || config.Password != "" {
			return nil, fmt.Errorf("smtp mail: credentials supplied while authentication is disabled")
		}
	case AuthenticationAuto, AuthenticationPlain, AuthenticationLogin:
		if config.Username == "" {
			return nil, fmt.Errorf("smtp mail: authentication requires a username")
		}
		if config.Security == SecurityNone {
			return nil, fmt.Errorf("smtp mail: authentication requires TLS")
		}
	default:
		return nil, fmt.Errorf("smtp mail: unknown authentication mode %q", config.Authentication)
	}

	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}
	if config.Timeout < 0 {
		return nil, fmt.Errorf("smtp mail: timeout must not be negative")
	}
	tlsConfig := config.TLSConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	}
	tlsConfig = tlsConfig.Clone()
	if tlsConfig.ServerName == "" {
		tlsConfig.ServerName = config.ServerName
	}
	if tlsConfig.MinVersion == 0 {
		tlsConfig.MinVersion = tls.VersionTLS12
	}

	composer, err := mail.NewComposer(mail.ComposerConfig{
		MessageIDDomain: config.MessageIDDomain,
		MaxMessageBytes: config.MaxMessageBytes,
		MaxRecipients:   config.MaxRecipients,
	})
	if err != nil {
		return nil, err
	}
	return &Sender{
		address:        config.Address,
		serverName:     config.ServerName,
		localName:      config.LocalName,
		security:       config.Security,
		tlsConfig:      tlsConfig,
		username:       config.Username,
		password:       config.Password,
		authentication: config.Authentication,
		timeout:        config.Timeout,
		composer:       composer,
	}, nil
}

func (s *Sender) Capabilities() mail.Capabilities {
	return s.composer.Capabilities()
}

func (s *Sender) Send(ctx context.Context, message mail.Message) (mail.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return mail.Receipt{}, mail.Error("send", err)
	}
	delivery, err := s.composer.Compose(message)
	if err != nil {
		return mail.Receipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return mail.Receipt{}, mail.Error("send", err)
	}

	client, cleanup, err := s.connect(ctx)
	if err != nil {
		return mail.Receipt{}, err
	}
	defer cleanup()

	if err := client.Mail(delivery.EnvelopeFrom); err != nil {
		return mail.Receipt{}, s.smtpError(ctx, "mail-from", err, false)
	}
	for _, recipient := range delivery.Recipients {
		if err := client.Rcpt(recipient); err != nil {
			return mail.Receipt{}, s.smtpError(ctx, "recipient", err, false)
		}
	}
	data, err := client.Data()
	if err != nil {
		return mail.Receipt{}, s.smtpError(ctx, "data", err, false)
	}
	if _, err := data.Write(delivery.Data); err != nil {
		_ = data.Close()
		return mail.Receipt{}, s.smtpError(ctx, "write", err, true)
	}
	if err := data.Close(); err != nil {
		return mail.Receipt{}, s.smtpError(ctx, "commit", err, true)
	}
	// A successful DATA close is the SMTP acceptance boundary. A later QUIT
	// failure must not invite callers to retry a message the server accepted.
	_ = client.Quit()
	return mail.Receipt{
		MessageID:  delivery.MessageID,
		Recipients: append([]string(nil), delivery.Recipients...),
	}, nil
}

// Test connects, negotiates security and authentication, then cleanly quits
// without submitting a message.
func (s *Sender) Test(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return mail.Error("test", err)
	}
	client, cleanup, err := s.connect(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := client.Quit(); err != nil {
		return s.smtpError(ctx, "quit", err, false)
	}
	return nil
}

func (s *Sender) connect(ctx context.Context) (*smtp.Client, func(), error) {
	dialer := &net.Dialer{Timeout: s.timeout}
	var (
		connection net.Conn
		err        error
	)
	if s.security == SecurityTLS {
		tlsDialer := tls.Dialer{NetDialer: dialer, Config: s.tlsConfig.Clone()}
		connection, err = tlsDialer.DialContext(ctx, "tcp", s.address)
		if err != nil {
			return nil, nil, mail.Error("connect", classify(ctx, mail.ErrTLS, err, false))
		}
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", s.address)
		if err != nil {
			return nil, nil, mail.Error("connect", classify(ctx, mail.ErrConnection, err, false))
		}
	}

	deadline := time.Now().Add(s.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		_ = connection.Close()
		return nil, nil, mail.Error("connect", classify(ctx, mail.ErrConnection, err, false))
	}

	stopWatcher := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-stopWatcher:
		}
	}()
	var stopWatcherOnce sync.Once
	stop := func() {
		stopWatcherOnce.Do(func() {
			close(stopWatcher)
		})
	}

	client, err := smtp.NewClient(connection, s.serverName)
	if err != nil {
		stop()
		_ = connection.Close()
		return nil, nil, mail.Error("greeting", classify(ctx, mail.ErrConnection, err, false))
	}
	cleanup := func() {
		stop()
		_ = client.Close()
	}

	if s.localName != "" {
		if err := client.Hello(s.localName); err != nil {
			cleanup()
			return nil, nil, mail.Error("hello", classify(ctx, mail.ErrConnection, err, false))
		}
	}
	if s.security == SecuritySTARTTLS {
		if supported, _ := client.Extension("STARTTLS"); !supported {
			cleanup()
			return nil, nil, mail.Error("starttls", mail.ErrTLS)
		}
		if err := client.StartTLS(s.tlsConfig.Clone()); err != nil {
			cleanup()
			return nil, nil, mail.Error("starttls", classify(ctx, mail.ErrTLS, err, false))
		}
	}
	if s.authentication != AuthenticationNone {
		auth, err := s.auth(client)
		if err != nil {
			cleanup()
			return nil, nil, mail.Error("authenticate", err)
		}
		if err := client.Auth(auth); err != nil {
			cleanup()
			return nil, nil, mail.Error("authenticate", classify(ctx, mail.ErrAuthentication, err, false))
		}
	}
	return client, cleanup, nil
}

func (s *Sender) auth(client *smtp.Client) (smtp.Auth, error) {
	_, advertised := client.Extension("AUTH")
	mechanisms := strings.Fields(strings.ToUpper(advertised))
	has := func(wanted string) bool {
		for _, mechanism := range mechanisms {
			if mechanism == wanted {
				return true
			}
		}
		return false
	}

	switch s.authentication {
	case AuthenticationPlain:
		if !has("PLAIN") {
			return nil, fmt.Errorf("%w: server does not advertise PLAIN", mail.ErrAuthentication)
		}
		return smtp.PlainAuth("", s.username, s.password, s.serverName), nil
	case AuthenticationLogin:
		if !has("LOGIN") {
			return nil, fmt.Errorf("%w: server does not advertise LOGIN", mail.ErrAuthentication)
		}
		return &loginAuth{username: s.username, password: s.password}, nil
	case AuthenticationAuto:
		if has("PLAIN") {
			return smtp.PlainAuth("", s.username, s.password, s.serverName), nil
		}
		if has("LOGIN") {
			return &loginAuth{username: s.username, password: s.password}, nil
		}
		return nil, fmt.Errorf("%w: server offers no supported mechanism", mail.ErrAuthentication)
	default:
		return nil, mail.ErrAuthentication
	}
}

func (s *Sender) smtpError(
	ctx context.Context,
	operation string,
	err error,
	acceptancePossible bool,
) error {
	sentinel := mail.ErrConnection
	var protocolError *textproto.Error
	if errors.As(err, &protocolError) && protocolError.Code >= 400 {
		sentinel = mail.ErrRejected
	}
	return mail.Error(operation, classify(ctx, sentinel, err, acceptancePossible))
}

func classify(ctx context.Context, sentinel, err error, acceptancePossible bool) error {
	outcome, protocolWasDefinitive := protocolOutcome(err)
	if !protocolWasDefinitive {
		outcome = transportOutcome(sentinel, err, acceptancePossible)
	}
	cause := err
	if contextErr := ctx.Err(); contextErr != nil && !protocolWasDefinitive {
		cause = contextErr
		if acceptancePossible {
			outcome = mail.OutcomeAcceptanceUncertain
		} else {
			outcome = mail.OutcomeTemporary
		}
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) && !protocolWasDefinitive {
		cause = context.DeadlineExceeded
		if acceptancePossible {
			outcome = mail.OutcomeAcceptanceUncertain
		} else {
			outcome = mail.OutcomeTemporary
		}
	}
	return mail.WithOutcome(outcome, fmt.Errorf("%w: %w", sentinel, cause))
}

func transportOutcome(sentinel, err error, acceptancePossible bool) mail.Outcome {
	if outcome, ok := protocolOutcome(err); ok {
		return outcome
	}
	if acceptancePossible {
		return mail.OutcomeAcceptanceUncertain
	}

	var networkError net.Error
	if errors.As(err, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return mail.OutcomeTemporary
	}
	if errors.Is(sentinel, mail.ErrConnection) {
		return mail.OutcomeTemporary
	}
	return mail.OutcomePermanent
}

func protocolOutcome(err error) (mail.Outcome, bool) {
	var protocolError *textproto.Error
	if errors.As(err, &protocolError) {
		switch {
		case protocolError.Code >= 400 && protocolError.Code < 500:
			return mail.OutcomeTemporary, true
		case protocolError.Code >= 500 && protocolError.Code < 600:
			return mail.OutcomePermanent, true
		}
	}
	return mail.OutcomeUnknown, false
}

type loginAuth struct {
	username string
	password string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, fmt.Errorf("%w: LOGIN requires TLS", mail.ErrAuthentication)
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(challenge))) {
	case "username:", "username":
		return []byte(a.username), nil
	case "password:", "password":
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("%w: unknown LOGIN challenge", mail.ErrAuthentication)
	}
}

var (
	_ mail.Sender = (*Sender)(nil)
	_ mail.Tester = (*Sender)(nil)
)

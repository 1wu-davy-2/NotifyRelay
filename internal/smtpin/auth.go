package smtpin

import (
	"crypto/subtle"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// authenticator checks SMTP AUTH credentials.
//
// The allow list of source IPs is the primary control; AUTH is an additional
// layer for deployments that cannot pin source addresses.
type authenticator struct {
	username string
	password string
}

func newAuthenticator(username, password string) *authenticator {
	return &authenticator{username: username, password: password}
}

// verify compares in constant time.
//
// The username comparison is constant time as well: a length- or
// content-dependent early exit would let a caller enumerate valid usernames.
func (a *authenticator) verify(username, password string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(a.username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(password), []byte(a.password)) == 1
	return userOK && passOK
}

// authSession is returned instead of session when SMTP AUTH is enabled.
//
// Implementing AuthSession is what makes go-smtp advertise AUTH and require it
// before MAIL FROM. The plain session type deliberately does not implement it,
// so an IP-allow-listed deployment is not forced through AUTH.
type authSession struct {
	*session
}

func (s *authSession) AuthMechanisms() []string {
	return []string{sasl.Plain, sasl.Login}
}

func (s *authSession) Auth(mech string) (sasl.Server, error) {
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(func(_, username, password string) error {
			if !s.server.auth.verify(username, password) {
				return smtp.ErrAuthFailed
			}
			return nil
		}), nil

	case sasl.Login:
		return &loginServer{
			authenticate: func(username, password string) error {
				if !s.server.auth.verify(username, password) {
					return smtp.ErrAuthFailed
				}
				return nil
			},
		}, nil

	default:
		return nil, smtp.ErrAuthUnsupported
	}
}

// loginServer implements the server half of SASL LOGIN.
//
// It is hand-written because go-sasl ships only the client half
// (NewLoginClient). The mechanism is three plaintext exchanges with no
// cryptography of its own — go-smtp handles the base64 framing and refuses to
// offer AUTH before TLS — so there is nothing here that a library would make
// safer. PLAIN is preferred; LOGIN exists because older clients offer nothing
// else.
type loginServer struct {
	authenticate func(username, password string) error

	state    int
	username string
}

const authAbort = "*"

func (l *loginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	// The client may abort at any point by sending a bare "*".
	if string(response) == authAbort {
		return nil, true, smtp.ErrAuthFailed
	}

	switch l.state {
	case 0:
		l.state = 1
		// The trailing CRLF is added by go-smtp when it writes a challenge.
		return []byte("Username:"), false, nil

	case 1:
		l.username = string(response)
		l.state = 2
		return []byte("Password:"), false, nil

	case 2:
		if err := l.authenticate(l.username, string(response)); err != nil {
			return nil, true, err
		}
		return nil, true, nil

	default:
		return nil, true, smtp.ErrAuthFailed
	}
}

package smtpin

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"strings"

	"notifyrelay/internal/message"
)

// toMessage converts a received RFC 5322 message into the unified model.
//
// The HTML part wins when both are present: the message model keeps a single
// body, and the router will downgrade it for channels that cannot render HTML.
func toMessage(raw []byte) (*message.Message, error) {
	parsed, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("not a valid RFC 5322 message: %w", err)
	}

	title := decodeHeader(parsed.Header.Get("Subject"))
	if strings.TrimSpace(title) == "" {
		// Mail does not require a subject, but the message model does. An
		// untitled notification beats rejecting the message outright.
		title = "(no subject)"
	}

	body, format := extractBody(parsed)
	// The wire format terminates the final line; that CRLF is framing, not
	// content, and carrying it into every downstream message is just noise.
	body = strings.TrimRight(body, "\r\n")

	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("message has no readable body")
	}

	msg := &message.Message{
		Title:    title,
		Body:     body,
		Format:   format,
		Type:     message.TypeInfo,
		Priority: message.PriorityDefault,
		Meta:     map[string]any{},
	}
	if from := parsed.Header.Get("From"); from != "" {
		msg.Meta["from"] = decodeHeader(from)
	}
	if date := parsed.Header.Get("Date"); date != "" {
		msg.Meta["date"] = date
	}

	return msg, nil
}

func extractBody(m *netmail.Message) (string, message.Format) {
	mediaType, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil {
		mediaType = "text/plain"
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		return readParts(multipart.NewReader(m.Body, params["boundary"]))
	}

	data, err := io.ReadAll(m.Body)
	if err != nil {
		return "", message.FormatText
	}
	decoded := decodeTransferEncoding(data, m.Header.Get("Content-Transfer-Encoding"))

	if mediaType == "text/html" {
		return string(decoded), message.FormatHTML
	}
	return string(decoded), message.FormatText
}

// readParts walks a multipart body, preferring the HTML part.
func readParts(mr *multipart.Reader) (string, message.Format) {
	var textBody string

	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}

		data, err := io.ReadAll(part)
		if err != nil {
			continue
		}

		mediaType, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			mediaType = "text/plain"
		}

		switch {
		case strings.HasPrefix(mediaType, "multipart/"):
			// A nested multipart, e.g. multipart/alternative inside
			// multipart/mixed. Recurse rather than giving up on the body.
			if nested, format := readParts(multipart.NewReader(bytes.NewReader(data), params["boundary"])); nested != "" {
				return nested, format
			}

		case mediaType == "text/html":
			return string(decodeTransferEncoding(data, part.Header.Get("Content-Transfer-Encoding"))), message.FormatHTML

		case mediaType == "text/plain" && textBody == "":
			textBody = string(decodeTransferEncoding(data, part.Header.Get("Content-Transfer-Encoding")))
		}
	}

	return textBody, message.FormatText
}

func decodeTransferEncoding(data []byte, encoding string) []byte {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
		if err != nil {
			return data
		}
		return decoded

	case "base64":
		decoded := make([]byte, base64.StdEncoding.DecodedLen(len(data)))
		n, err := base64.StdEncoding.Decode(decoded, bytes.TrimSpace(data))
		if err != nil {
			return data
		}
		return decoded[:n]

	default:
		return data
	}
}

// decodeHeader decodes RFC 2047 encoded words, so a UTF-8 subject arrives as
// readable text rather than as =?utf-8?q?...?= .
func decodeHeader(s string) string {
	if s == "" {
		return ""
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(s)
	if err != nil {
		return s
	}
	return decoded
}

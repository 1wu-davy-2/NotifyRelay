package dingtalk

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
)

// sign computes the DingTalk robot signature.
//
// The algorithm is DingTalk's own, and differs from Feishu's in a way that is
// easy to get backwards:
//
//	stringToSign = "{timestamp}\n{secret}"
//	sign         = base64(HMAC-SHA256(key=secret, message=stringToSign))
//
// The secret is the KEY and the timestamp line is the MESSAGE. Feishu is the
// other way round.
func sign(secret string, timestampMillis int64) string {
	stringToSign := strconv.FormatInt(timestampMillis, 10) + "\n" + secret

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stringToSign))

	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// signedURL appends the timestamp and signature to a robot webhook URL.
//
// The signature is base64, which contains '+', '/' and '=', so it must be
// escaped before it goes into a query string.
func signedURL(webhookURL, secret string, timestampMillis int64) (string, error) {
	if secret == "" {
		return webhookURL, nil
	}

	parsed, err := url.Parse(webhookURL)
	if err != nil {
		return "", fmt.Errorf("parse webhook url: %w", err)
	}

	query := parsed.Query()
	query.Set("timestamp", strconv.FormatInt(timestampMillis, 10))
	query.Set("sign", sign(secret, timestampMillis))
	parsed.RawQuery = query.Encode()

	return parsed.String(), nil
}

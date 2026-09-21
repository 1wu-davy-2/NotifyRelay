package feishu

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
)

// sign computes the Feishu custom-bot signature.
//
// Feishu's algorithm is the mirror image of DingTalk's, which is exactly the
// kind of difference that gets copied wrong:
//
//	stringToSign = "{timestamp}\n{secret}"
//	sign         = base64(HMAC-SHA256(key=stringToSign, message=<empty>))
//
// The timestamp line is the KEY and the message is empty. DingTalk uses the
// secret as the key and the timestamp line as the message.
func sign(secret string, timestampSeconds int64) string {
	stringToSign := strconv.FormatInt(timestampSeconds, 10) + "\n" + secret

	mac := hmac.New(sha256.New, []byte(stringToSign))
	// Deliberately no Write: the message is empty by specification.
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

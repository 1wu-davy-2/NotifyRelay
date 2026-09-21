package router

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/message"
)

// Apply enforces the length limits a channel declares.
//
// It returns one or more messages: the core never hands a channel a message
// that the channel has already said it cannot render.
//
// Two limits are enforced, because the IM platforms impose two different
// kinds. DingTalk caps a robot message at 20000 bytes and WeCom at 4096 bytes;
// a rune count cannot express either, since one Chinese character and one
// ASCII letter are both "one character" to a rune counter but three bytes
// apart on the wire. Nothing is measured in runes alone and then hoped to fit.
func Apply(msg *message.Message, cap channel.Capability) ([]*message.Message, error) {
	out := *msg

	if cap.TitleMaxLen > 0 {
		out.Title = truncateRunes(out.Title, cap.TitleMaxLen)
	}

	budget, ok := bodyBudget(cap, &out)
	if !ok {
		return nil, fmt.Errorf(
			"router: the title and channel markup already exceed the channel limit of %d characters / %d bytes",
			cap.BodyMaxLen, cap.BodyMaxBytes)
	}

	if fits(out.Body, budget) {
		return []*message.Message{&out}, nil
	}

	switch cap.OverflowMode {
	case channel.OverflowError:
		return nil, fmt.Errorf("router: body is %d characters / %d bytes but the channel allows %d/%d once its own markup and the title are accounted for",
			utf8.RuneCountInString(out.Body), len(out.Body), budget.runes, budget.bytes)

	case channel.OverflowTruncate:
		out.Body = truncateBody(out.Body, budget)
		return []*message.Message{&out}, nil

	case channel.OverflowSplit:
		chunks := splitBody(out.Body, budget)
		parts := make([]*message.Message, 0, len(chunks))
		for i, chunk := range chunks {
			part := out
			part.Body = chunk
			if len(chunks) > 1 {
				part.Title = fmt.Sprintf("%s [%d/%d]", out.Title, i+1, len(chunks))
				// The part marker can push the title past its own limit.
				if cap.TitleMaxLen > 0 {
					part.Title = truncateRunes(part.Title, cap.TitleMaxLen)
				}
			}
			parts = append(parts, &part)
		}
		return parts, nil

	default:
		return nil, fmt.Errorf("router: unknown overflow mode %d for this channel", cap.OverflowMode)
	}
}

// limit is a pair of budgets for the body once everything the channel adds
// around it has been subtracted. A zero field means unlimited.
type limit struct {
	runes int
	bytes int
}

// bodyBudget works out how much room is left for the body.
//
// The channel's declared limits describe the payload the platform receives,
// which is the body plus the title, the links and whatever markup the channel
// wraps around it. Subtracting those here is what makes the declared limit a
// guarantee about the message that actually leaves the process, rather than a
// guarantee about a fragment of it.
func bodyBudget(cap channel.Capability, msg *message.Message) (limit, bool) {
	var titleRunes, titleBytes, linkRunes, linkBytes int

	titleRunes = utf8.RuneCountInString(msg.Title)
	titleBytes = len(msg.Title)

	for _, l := range msg.Links {
		n := utf8.RuneCountInString(l.Text) + utf8.RuneCountInString(l.URL) + 4 // "[](…)" and a newline
		linkRunes += n
		linkBytes += len(l.Text) + len(l.URL) + 4
	}

	// The "[12/34] " marker a split appends to the title.
	const splitMarkerRunes, splitMarkerBytes = 12, 12

	fixedRunes := cap.PayloadOverheadRunes + titleRunes + linkRunes + splitMarkerRunes
	fixedBytes := cap.PayloadOverheadBytes + titleBytes + linkBytes + splitMarkerBytes

	budget := limit{}
	if cap.BodyMaxLen > 0 {
		budget.runes = cap.BodyMaxLen - fixedRunes
		if budget.runes <= 0 {
			return limit{}, false
		}
	}
	if cap.BodyMaxBytes > 0 {
		budget.bytes = cap.BodyMaxBytes - fixedBytes
		if budget.bytes <= 0 {
			return limit{}, false
		}
	}

	return budget, true
}

// fits reports whether s satisfies both budgets.
func fits(s string, l limit) bool {
	if l.runes > 0 && utf8.RuneCountInString(s) > l.runes {
		return false
	}
	if l.bytes > 0 && len(s) > l.bytes {
		return false
	}
	return true
}

func truncateBody(s string, l limit) string {
	runes := []rune(s)

	end := len(runes)
	if l.runes > 0 && end > l.runes {
		end = l.runes
	}

	if l.bytes > 0 {
		used := 0
		for i := 0; i < end; i++ {
			size := runeSize(runes[i])
			if used+size > l.bytes {
				end = i
				break
			}
			used += size
		}
	}

	return string(runes[:end])
}

// splitBody cuts s into chunks satisfying both budgets, preferring to break at
// a line boundary so a split does not land mid-sentence when it can be avoided.
func splitBody(s string, l limit) []string {
	runes := []rune(s)
	var chunks []string

	for start := 0; start < len(runes); {
		end, used := start, 0

		for end < len(runes) {
			if l.runes > 0 && end-start+1 > l.runes {
				break
			}
			size := runeSize(runes[end])
			if l.bytes > 0 && used+size > l.bytes {
				break
			}
			used += size
			end++
		}

		if end == start {
			// A single character exceeds the byte budget on its own. Take it
			// rather than spinning forever on an unsatisfiable constraint.
			end = start + 1
		}

		cut := end
		// Search the second half of the window for a line break.
		for i := end - 1; i > start+(end-start)/2; i-- {
			if runes[i] == '\n' {
				cut = i + 1
				break
			}
		}

		chunks = append(chunks, strings.TrimRight(string(runes[start:cut]), "\n"))
		start = cut
	}

	return chunks
}

func runeSize(r rune) int {
	if size := utf8.RuneLen(r); size > 0 {
		return size
	}
	return 1
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit])
}

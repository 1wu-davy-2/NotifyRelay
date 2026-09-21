package router

import (
	"notifyrelay/internal/channel"
	"notifyrelay/internal/message"
	"notifyrelay/internal/render"
)

// Adapt rewrites msg so that its Format is one the channel renders natively.
//
// The design documents call this the "format downgrade". It usually downgrades,
// but a markdown message addressed to a channel that renders HTML is upgraded,
// because rendering markdown as plain text there would be a needless loss of
// fidelity (mail being the obvious case).
//
// Selection rule, for declared format F and the channel's supported set S:
//
//	F in S                        -> keep F
//	F=markdown, html in S         -> markdown to HTML
//	F=markdown, otherwise         -> markdown to plain text
//	F=html                        -> html to plain text
//	F=text                        -> keep text (never upgraded)
//
// The input is not mutated; a copy is returned.
func Adapt(msg *message.Message, cap channel.Capability) *message.Message {
	out := *msg

	if out.Format == "" {
		out.Format = message.FormatText
	}
	if supports(cap, out.Format) {
		// The channel renders this format, but "markdown" is not one format.
		// Converting to the channel's dialect is the core's job so that no
		// channel implementation contains a converter of its own.
		if out.Format == message.FormatMarkdown && cap.MarkdownDialect != render.DialectCommonMark {
			out.Body = render.Markdown(out.Body, cap.MarkdownDialect)
		}
		return &out
	}

	switch out.Format {
	case message.FormatMarkdown:
		if supports(cap, message.FormatHTML) {
			out.Body = render.MarkdownToHTML(out.Body)
			out.Format = message.FormatHTML
		} else {
			out.Body = render.MarkdownToText(out.Body)
			out.Format = message.FormatText
		}

	case message.FormatHTML:
		// TODO(M3): emit real markdown when a markdown-only channel (DingTalk,
		// Feishu) needs it. Plain text is a fidelity loss, never a failure.
		out.Body = render.HTMLToText(out.Body)
		out.Format = message.FormatText

	default:
		out.Body = msg.Body
		out.Format = message.FormatText
	}

	return &out
}

// supports reports whether the channel renders f natively.
//
// A channel that declares no formats is treated as text-only, so a channel
// author who forgets SupportedFormats gets the safe behaviour rather than
// receiving markup it cannot render.
func supports(cap channel.Capability, f message.Format) bool {
	if len(cap.SupportedFormats) == 0 {
		return f == message.FormatText
	}
	return cap.Supports(f)
}

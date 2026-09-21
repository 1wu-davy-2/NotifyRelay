// Package all pulls the built-in channel implementations into the binary.
//
// Adding a channel type is: create internal/channel/<name>/ implementing
// channel.Channel (registering itself from an init()), then add one blank
// import below. Nothing in internal/router, internal/api or internal/message
// changes.
//
// This aggregator lives in its own package rather than in a file inside
// package channel because channel implementations must import package channel
// for the Channel interface — a file in that package could not import them
// without creating an import cycle.
//
// Keep this list in sync with the NOTICE file.
package all

import (
	// Blank imports trigger each channel package's init(), which calls
	// channel.Register.
	_ "notifyrelay/internal/channel/dingtalk"
	_ "notifyrelay/internal/channel/email"
	_ "notifyrelay/internal/channel/feishu"
	_ "notifyrelay/internal/channel/slack"
	_ "notifyrelay/internal/channel/wecom"
	_ "notifyrelay/internal/channel/webhook"
)

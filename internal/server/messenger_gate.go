package server

import "github.com/open-octo/octo-agent/internal/channel"

// imChannelsConfigured reports whether ~/.octo/channels.yml enables at least
// one IM platform. It gates the send_message / send_file tools.
func imChannelsConfigured() bool {
	cfg, err := channel.LoadConfig()
	if err != nil || cfg == nil {
		return false
	}
	return len(cfg.EnabledPlatforms()) > 0
}

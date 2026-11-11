package tools

import (
	"context"
	"testing"
)

type nopMessenger struct{}

func (nopMessenger) SendMessage(platform, chatID, text string) error    { return nil }
func (nopMessenger) SendFile(platform, chatID, path, name string) error { return nil }
func (nopMessenger) KnownChats() []KnownRecipient                       { return nil }

func TestMessengerAvailableGatesIMTools(t *testing.T) {
	SetMessenger(nopMessenger{})
	t.Cleanup(func() { SetMessenger(nil); SetMessengerAvailable(nil) })
	has := func() (msg, file bool) {
		for _, d := range defaultToolsFor(context.Background(), "", 0) {
			switch d.Name {
			case "send_message":
				msg = true
			case "send_file":
				file = true
			}
		}
		return
	}
	SetMessengerAvailable(func() bool { return false })
	if m, f := has(); m || f {
		t.Fatalf("IM tools should be hidden without channels: send_message=%v send_file=%v", m, f)
	}
	SetMessengerAvailable(func() bool { return true })
	if m, f := has(); !m || !f {
		t.Fatalf("IM tools should be advertised with channels: send_message=%v send_file=%v", m, f)
	}
}

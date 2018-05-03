package xmpp

import (
	"errors"
	"github.com/mattn/go-xmpp"
	"github.com/oklahomer/go-sarah"
	"golang.org/x/net/context"
	"testing"
	"time"
)

type DummyStanzaReceiver struct {
	RecvFunc func() (interface{}, error)
}

func (sr *DummyStanzaReceiver) Recv() (interface{}, error) {
	return sr.RecvFunc()
}

var _ stanzaReceiver = (*DummyStanzaReceiver)(nil)

type DummyStanzaSender struct {
	SendFunc    func(xmpp.Chat) (int, error)
	PingC2SFunc func(string, string) error
}

func (ss *DummyStanzaSender) Send(chat xmpp.Chat) (int, error) {
	return ss.SendFunc(chat)
}

func (ss *DummyStanzaSender) PingC2S(jid, server string) error {
	return ss.PingC2SFunc(jid, server)
}

func TestAdapter_receiveStanza(t *testing.T) {
	input := "dummy stanza for comparison"
	received := make(chan Stanza)
	adapter := &Adapter{
		stanzaHandler: func(_ context.Context, _ *Config, stanza Stanza, _ func(sarah.Input) error) {
			received <- stanza
		},
	}

	receiver := &DummyStanzaReceiver{
		RecvFunc: func() (interface{}, error) {
			return input, nil
		},
	}

	rootCtx := context.Background()
	ctx, cancel := context.WithCancel(rootCtx)
	defer cancel()

	go adapter.receiveStanza(ctx, receiver, make(chan struct{}), func(_ sarah.Input) error { return nil })

	select {
	case stanza := <-received:
		if stanza != input {
			t.Errorf("Unexpected input is passed: %#v.", stanza)
		}

	case <-time.NewTimer(1 * time.Second).C:
		t.Error("StanzaHandler is not called.")

	}
}

func TestAdapter_receiveStanza_Error(t *testing.T) {
	adapter := &Adapter{
		stanzaHandler: func(_ context.Context, _ *Config, _ Stanza, _ func(sarah.Input) error) {
			t.Fatal("StanzaHandler should not be called.")
		},
	}

	receiver := &DummyStanzaReceiver{
		RecvFunc: func() (interface{}, error) {
			return nil, errors.New("dummy")
		},
	}

	tryPing := make(chan struct{}, 1)

	rootCtx := context.Background()
	ctx, cancel := context.WithCancel(rootCtx)
	defer cancel()

	go adapter.receiveStanza(ctx, receiver, tryPing, func(_ sarah.Input) error { return nil })

	select {
	case <-tryPing:
		// O.K.

	case <-time.NewTimer(1 * time.Second).C:
		t.Error("Ping signal is not sent.")

	}
}

func TestAdapter_superviseConnection(t *testing.T) {
	send := make(chan struct{}, 1)
	ping := make(chan struct{}, 1)
	sender := &DummyStanzaSender{
		SendFunc: func(chat xmpp.Chat) (int, error) {
			send <- struct{}{}
			return 1, nil
		},
		PingC2SFunc: func(_, _ string) error {
			select {
			case ping <- struct{}{}:
				// O.K.

			default:
				// Duplicate entry. Just ignore.

			}
			return nil
		},
	}
	rootCtx := context.Background()
	ctx, cancel := context.WithCancel(rootCtx)

	pingInterval := 10 * time.Millisecond
	adapter := &Adapter{
		config: &Config{
			PingInterval: pingInterval,
		},
		messageQueue: make(chan xmpp.Chat, 1),
	}

	conErr := make(chan error)
	go func() {
		err := adapter.superviseConnection(ctx, sender, make(chan struct{}, 1))
		conErr <- err
	}()

	adapter.messageQueue <- xmpp.Chat{}

	time.Sleep(pingInterval + 10*time.Millisecond) // Give long enough time to check ping.

	cancel()
	select {
	case <-send:
		// O.K.

	case <-time.NewTimer(10 * time.Second).C:
		t.Error("Connection.Send was not called.")

	}

	select {
	case <-ping:
		// O.K.

	case <-time.NewTimer(10 * time.Second).C:
		t.Error("Connection.Ping was not called.")

	}

	select {
	case err := <-conErr:
		if err != nil {
			t.Errorf("Unexpected error was returned: %s.", err.Error())
		}

	case <-time.NewTimer(10 * time.Second).C:
		t.Error("Context was canceled, but superviseConnection did not return.")

	}
}

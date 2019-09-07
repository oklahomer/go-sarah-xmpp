package xmpp

import (
	"context"
	"errors"
	"github.com/mattn/go-xmpp"
	"github.com/oklahomer/go-sarah/v2"
	"reflect"
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

func TestRespWithNext(t *testing.T) {
	fnc := func(_ context.Context, _ sarah.Input) (*sarah.CommandResponse, error) {
		return nil, nil
	}
	opt := RespWithNext(fnc)

	options := &respOptions{}
	opt(options)

	if options.userContext.Next == nil {
		t.Fatal("Expected function is not set.")
	}

	if reflect.ValueOf(options.userContext.Next).Pointer() != reflect.ValueOf(fnc).Pointer() {
		t.Error("Expected function is not set.")
	}
}

func TestRespWithNextSerializable(t *testing.T) {
	arg := &sarah.SerializableArgument{
		FuncIdentifier: "foo",
		Argument:       struct{}{},
	}
	opt := RespWithNextSerializable(arg)

	options := &respOptions{}
	opt(options)

	if options.userContext.Serializable == nil {
		t.Fatal("Expected argument is not set.")
	}

	if options.userContext.Serializable != arg {
		t.Errorf("Expected argument is not set: %+v", options.userContext.Serializable)
	}
}

type invalidInput struct {
}

func (i invalidInput) SenderKey() string {
	return ""
}

func (i invalidInput) Message() string {
	return ""
}

func (i invalidInput) SentAt() time.Time {
	return time.Now()
}

func (i invalidInput) ReplyTo() sarah.OutputDestination {
	return nil
}

var _ sarah.Input = (*invalidInput)(nil)

func TestNewResponse(t *testing.T) {
	t.Run("Invalid sarah.Input", func(t *testing.T) {
		input := &invalidInput{}
		_, err := NewResponse(input, "dummy")

		if err == nil {
			t.Error("Expected error is not returned.")
		}
	})

	t.Run("Valid sarah.Input and options", func(t *testing.T) {
		input := &MessageInput{
			Event: &xmpp.Chat{
				Remote: "replyTo",
			},
		}
		message := "dummy"
		arg := &sarah.SerializableArgument{
			FuncIdentifier: "foo",
			Argument:       struct{}{},
		}

		response, err := NewResponse(input, message, RespWithNextSerializable(arg))

		if err != nil {
			t.Fatalf("Unexpected error is returned: %s", err)
		}

		content, ok := response.Content.(xmpp.Chat)
		if !ok {
			t.Fatalf("Response content is not type of xmpp.Chat: %T", response.Content)
		}
		if content.Text != message {
			t.Errorf("Expected message is not set: %s", content.Text)
		}
		if response.UserContext.Serializable != arg {
			t.Errorf("Unexpected user context is set: %+v", response.UserContext.Serializable)

		}
	})
}

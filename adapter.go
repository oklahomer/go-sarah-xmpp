package xmpp

import (
	"crypto/tls"
	"strings"
	"time"

	"github.com/jpillora/backoff"
	"github.com/mattn/go-xmpp"
	"github.com/oklahomer/go-sarah"
	"github.com/oklahomer/go-sarah/log"

	"golang.org/x/net/context"
)

const (
	XMPP sarah.BotType = "xmpp"
)

// AdapterOption defines function signature that Adapter's
// functional option must satisfy.
type AdapterOption func(adapter *Adapter) error

// Adapter offers Bot developers easy way to communicate with Xmpp.
// This implements sarah.Adapter interface, so this instance can be fed
// to sarah.Runner as below.
type Adapter struct {
	config         *Config
	client         *xmpp.Client
	payloadHandler func(context.Context, *Config, DecodedPayload, func(sarah.Input) error)
}

func xmppPayloadHandler(fnc func(context.Context, *Config, DecodedPayload, func(sarah.Input) error)) AdapterOption {
	return func(adapter *Adapter) error {
		adapter.payloadHandler = fnc
		return nil
	}
}

// NewAdapter creates new Adapter with given *Config and zero or more AdapterOption.
func NewAdapter(config *Config, options ...AdapterOption) (*Adapter, error) {
	var err error
	adapter := &Adapter{
		config:         config,
		payloadHandler: xmppPayloadHandler,
	}

	for _, opt := range options {
		err = opt(adapter)
		if err != nil {
			return nil, err
		}
	}

	return adapter, nil
}

// BotType returns BotType of this particular instance.
func (adapter *Adapter) BotType() sarah.BotType {
	return XMPP
}

// Set up and watch the xmpp connection
func (adapter *Adapter) Run(ctx context.Context, enqueueInput func(sarah.Input) error, notifyErr func(error)) {
	var err error

	err = adapter.createXMPP()
	if err != nil {
		notifyErr(sarah.NewBotNonContinuableError(err.Error()))
	}

	go func() {
		initial := true
		bf := &backoff.Backoff{
			Min:    time.Second,
			Max:    5 * time.Minute,
			Jitter: true,
		}
		for {
			if initial {
				adapter.handleXMPP(enqueueInput, notifyErr)
				initial = false
			}
			d := bf.Duration()
			//b.Log.Infof("Disconnected. Reconnecting in %s", d)
			time.Sleep(d)
			err = adapter.createXMPP()
			if err == nil {
				//b.Remote <- config.Message{Username: "system", Text: "rejoin", Channel: "", Account: b.Account, Event: config.EVENT_REJOIN_CHANNELS}
				adapter.handleXMPP(enqueueInput, notifyErr)
				bf.Reset()
			}
		}
	}()

}

func (adapter *Adapter) createXMPP() error {
	var err error
	tc := new(tls.Config)
	tc.InsecureSkipVerify = adapter.config.SkipTLSVerify
	tc.ServerName = strings.Split(adapter.config.Server, ":")[0]
	options := xmpp.Options{
		Host:                         adapter.config.Server,
		User:                         adapter.config.Jid,
		Password:                     adapter.config.Password,
		NoTLS:                        true,
		StartTLS:                     true,
		TLSConfig:                    tc,
		Debug:                        adapter.config.Debug,
		Session:                      true,
		Status:                       "",
		StatusMessage:                "",
		Resource:                     "",
		InsecureAllowUnencryptedAuth: false,
	}

	adapter.client, err = options.NewClient()
	return err
}

// Listens for and handles incoming XMPP messages
// Push incoming messages to sarah via enqueueInput
// Notify sarah of errors on notifyErr
func (adapter *Adapter) handleXMPP(enqueueInput func(sarah.Input) error, notifyErr func(error)) error {
	done := adapter.xmppKeepAlive()
	defer close(done)
	for {
		m, err := adapter.client.Recv()
		if err != nil {
			return err
		}
		switch v := m.(type) {
		case xmpp.Chat:
			if adapter.skipMessage(v) {
				continue
			}

			// TODO consider handling MUC and chat differently??

			input := NewMessageInput(v)

			trimmed := strings.TrimSpace(input.Message())
			if adapter.config.HelpCommand != "" && trimmed == adapter.config.HelpCommand {
				// Help command
				help := sarah.NewHelpInput(input.SenderKey(), input.Message(), input.SentAt(), input.ReplyTo())
				enqueueInput(help)
			} else if adapter.config.AbortCommand != "" && trimmed == adapter.config.AbortCommand {
				// Abort command
				abort := sarah.NewAbortInput(input.SenderKey(), input.Message(), input.SentAt(), input.ReplyTo())
				enqueueInput(abort)
			} else {
				// Regular input
				enqueueInput(input)
			}

		case xmpp.Presence:
			// do nothing
		}
	}
}

func (adapter *Adapter) xmppKeepAlive() chan bool {
	done := make(chan bool)
	var err error
	go func() {
		ticker := time.NewTicker(adapter.config.PingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				//b.Log.Debugf("PING")
				err = adapter.client.PingC2S("", "")
				if err != nil {
					//	b.Log.Debugf("PING failed %#v", err)
				}
			case <-done:
				return
			}
		}
	}()
	return done
}

// skipMessage skips messages that need to be skipped
func (adapter *Adapter) skipMessage(message xmpp.Chat) bool {
	// Skip messages to ourselves
	if adapter.parseNick(message.Remote) == adapter.parseNick(adapter.config.Jid) {
		return true
	}

	// skip empty messages
	if message.Text == "" {
		return true
	}

	// skip subject messages
	if strings.Contains(message.Text, "</subject>") {
		return true
	}

	// skip delayed messages
	t := time.Time{}
	return message.Stamp != t
}

func (adapter *Adapter) parseNick(remote string) string {
	s := strings.Split(remote, "@")
	if len(s) > 0 {
		s = strings.Split(s[1], "/")
		if len(s) == 2 {
			return s[1] // nick
		}
	}
	return ""
}

func (adapter *Adapter) parseChannel(remote string) string {
	s := strings.Split(remote, "@")
	if len(s) >= 2 {
		return s[0] // channel
	}
	return ""
}

// SendMessage let Bot send message to Xmpp
func (adapter *Adapter) SendMessage(ctx context.Context, output sarah.Output) {
	switch content := output.Content().(type) {
	case string:

		message := xmpp.Chat{
			Remote: output.Destination().(string),
			Text:   content,
			//Thread: ctx.Value().(string),
		}

		if _, err := adapter.client.Send(message); err != nil {
			log.Error("something went wrong with xmpp send stanza", err)
		}

	case *sarah.CommandHelps:
		/*
		messages := []linebot.Message{}
		for _, commandHelp := range *content {
			messages = append(messages, linebot.NewTextMessage(commandHelp.InputExample))
		}
		adapter.client.Send(message)
		*/

	default:
		log.Warnf("unexpected output %#v", output)

	}
}


// NewStringResponse creates new sarah.CommandResponse instance with given string.
func NewStringResponse(responseContent string) *sarah.CommandResponse {
	return &sarah.CommandResponse{
		Content:     responseContent,
		UserContext: nil,
	}
}

// NewStringResponseWithNext creates new sarah.CommandResponse instance with given
// string and next function to continue
//
// With this method user context is directly stored as an anonymous function since
// Slack Bot works with single WebSocket connection and hence usually works with
// single process.
//
// To use external storage to store user context, use go-sarah-rediscontext or
// similar sarah.UserContextStorage implementation.
func NewStringResponseWithNext(responseContent string, next sarah.ContextualFunc) *sarah.CommandResponse {
	return &sarah.CommandResponse{
		Content:     responseContent,
		UserContext: sarah.NewUserContext(next),
	}
}


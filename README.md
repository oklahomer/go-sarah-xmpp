This is a [sarah](https://github.com/oklahomer/go-sarah) ```Adapter``` implementation for XMPP / Jabber

At present this is work in progress, and API may change without notice.

# Getting Started
Below is a minimal sample that describes how to setup and start XMPP Adapter.


```go
package main

import (
        "github.com/oklahomer/go-sarah"
        "github.com/oklahomer/go-sarah-xmpp"
        "golang.org/x/net/context"
        "gopkg.in/yaml.v2"
        "io/ioutil"
)

func main() {
        // Setup configuration
        configBuf, _ := ioutil.ReadFile("/path/to/adapter/config.yaml")
        xmppConfig := xmpp.NewConfig()
        yaml.Unmarshal(configBuf, xmppConfig)

        // Setup bot
        xmppAdapter, _ := xmpp.NewAdapter(xmppConfig)
        storage := sarah.NewUserContextStorage(sarah.NewCacheConfig())
        xmppBot, _ := sarah.NewBot(xmppAdapter, sarah.BotWithStorage(storage))
	
        // Start
        rootCtx := context.Background()
        runnerCtx, _ := context.WithCancel(rootCtx)
        runner, _ := sarah.NewRunner(sarah.NewConfig(), sarah.WithBot(xmppBot))
        runner.Run(runnerCtx)
}
```

## Acknowledgements and thanks
This library uses the excellent xmpp library - https://github.com/mattn/go-xmpp

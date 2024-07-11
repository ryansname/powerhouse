package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/go-resty/resty/v2"
	"github.com/gookit/ini/v2"
)

const deviceId = "wits-repeater"

var msgId uint16 = 0

type ResponseJson struct {
	Charts struct {
		PricesLastFiveMinsMap struct {
			Key  string `json:"key"`
			Data struct {
				Nodes []struct {
					Name          string  `json:"name"`
					TradingDate   string  `json:"trading_date"`
					TradingPeriod int     `json:"trading_period"`
					RunType       string  `json:"run_type"`
					GipGxpFull    string  `json:"gip_gxp_full"`
					Price         float64 `json:"price"`
					RunTime       string  `json:"run_time"`
					MarketTime    string  `json:"market_time"`
				}
			} `json:"data"`
		} `json:"prices_last_five_mins_map"`
	} `json:"charts"`
}

func makeClientConfig() (*autopaho.ClientConfig, error) {
	config := ini.New()
	err := config.LoadExists("/etc/wits-repeater.ini", "wits-repeater.ini")
	if err != nil {
		return nil, err
	}

	clientID := "wits-repeater"

	// We will connect to the Eclipse test server (note that you may see messages that other users publish)
	rawHost := config.String("mqtt.host")
	u, err := url.Parse("mqtt://" + rawHost + ":1883")
	if err != nil {
		return nil, err
	}
	fmt.Println(u)

	cliCfg := autopaho.ClientConfig{
		ServerUrls:      []*url.URL{u},
		ConnectUsername: config.String("mqtt.username"),
		ConnectPassword: []byte(config.String("mqtt.password")),
		KeepAlive:       20, // Keepalive message should be sent every 20 seconds
		// CleanStartOnInitialConnection defaults to false. Setting this to true will clear the session on the first connection.
		CleanStartOnInitialConnection: true,
		SessionExpiryInterval:         60,
		OnConnectionUp: func(cm *autopaho.ConnectionManager, connAck *paho.Connack) {
			fmt.Println("mqtt connection up")
			// Subscribing in the OnConnectionUp callback is recommended (ensures the subscription is reestablished if
			// the connection drops)
			if _, err := cm.Subscribe(context.Background(), &paho.Subscribe{
				Subscriptions: []paho.SubscribeOptions{
					{Topic: "potato", QoS: 1},
				},
			}); err != nil {
				fmt.Printf("failed to subscribe (%s). This is likely to mean no messages will be received.", err)
			}
			fmt.Println("mqtt subscription made")
		},
		OnConnectError: func(err error) { fmt.Printf("error whilst attempting connection: %s\n", err) },
		// eclipse/paho.golang/paho provides base mqtt functionality, the below config will be passed in for each connection
		ClientConfig: paho.ClientConfig{
			// If you are using QOS 1/2, then it's important to specify a client id (which must be unique)
			ClientID: clientID,
			// OnPublishReceived is a slice of functions that will be called when a message is received.
			// You can write the function(s) yourself or use the supplied Router
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				func(pr paho.PublishReceived) (bool, error) {
					fmt.Printf("received message on topic %s; body: %s (retain: %t)\n", pr.Packet.Topic, pr.Packet.Payload, pr.Packet.Retain)
					return true, nil
				}},
			OnClientError: func(err error) { fmt.Printf("client error: %s\n", err) },
			OnServerDisconnect: func(d *paho.Disconnect) {
				if d.Properties != nil {
					fmt.Printf("server requested disconnect: %s\n", d.Properties.ReasonString)
				} else {
					fmt.Printf("server requested disconnect; reason code: %d\n", d.ReasonCode)
				}
			},
		},
	}
	return &cliCfg, nil
}

func createEntity(client *autopaho.ConnectionManager, ctx context.Context, prettyName, id, measure string) error {
	type Config struct {
		Name          string  `json:"name,omitempty"`
		DeviceClass   string  `json:"device_class"`
		StateTopic    string  `json:"state_topic"`
		UnitOfMeasure *string `json:"unit_of_measure"`
		ValueTemplate string  `json:"value_template"`
		UniqueId      string  `json:"unique_id"`
		ExpireAfter   uint    `json:"expire_after,omitempty"`
		Device        struct {
			Identifiers  []string `json:"identifiers"`
			Name         string   `json:"name"`
			Manufacturer string   `json:"manufacturer,omitempty"`
			Model        string   `json:"model,omitempty"`
		} `json:"device"`
	}
	config := Config{}
	config.Name = prettyName
	config.DeviceClass = "monetary"
	config.StateTopic = "homeassistant/sensor/" + deviceId + "/state"
	config.UnitOfMeasure = &measure
	config.ValueTemplate = "{{ value_json." + id + "}}"
	config.UniqueId = "wits_" + id
	config.ExpireAfter = 60 * 10
	config.Device.Identifiers = []string{deviceId}
	config.Device.Name = "WITS - Wholesale Information Trading System"
	// config.Device.Manufacturer = "NZX & Electricity Authority"

	msgId += 1
	publish := paho.Publish{}
	publish.PacketID = msgId
	publish.QoS = 2
	publish.Topic = "homeassistant/sensor/" + deviceId + "/config"

	payloadString, err := json.Marshal(config)
	if err != nil {
		return err
	}
	publish.Payload = []byte(payloadString)

	client.Publish(ctx, &publish)
	return nil
}

func updateEntity(client *autopaho.ConnectionManager, ctx context.Context, id, value string) error {
	fmt.Printf("%s set to %s\n", id, value)
	data := map[string]string{
		id: value,
	}
	payloadString, err := json.Marshal(data)
	if err != nil {
		return err
	}

	msgId += 1

	publish := paho.Publish{}
	publish.PacketID = msgId
	publish.QoS = 2
	publish.Topic = "homeassistant/sensor/" + deviceId + "/state"
	publish.Payload = []byte(payloadString)

	client.Publish(ctx, &publish)
	return nil
}

func fetchWitsInfo() (*ResponseJson, error) {
	client := resty.New()

	resp, err := client.R().
		SetQueryParams(map[string]string{
			"chart_keys": "prices_last_five_mins_map",
		}).
		SetResult(ResponseJson{}).
		EnableTrace().
		Get("https://www2.electricityinfo.co.nz/dashboard/updates")

	if err != nil {
		return nil, err
	}
	return resp.Result().(*ResponseJson), nil
}

func logError(msg string, err error) {
	fmt.Fprint(os.Stderr, msg, err)
}

func main() {
	// App will run until cancelled by user (e.g. ctrl-c)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	clientConfig, err := makeClientConfig()
	if err != nil {
		panic(err)
	}

	c, err := autopaho.NewConnection(ctx, *clientConfig) // starts process; will reconnect until context cancelled
	if err != nil {
		panic(err)
	}
	// Wait for the connection to come up
	if err = c.AwaitConnection(ctx); err != nil {
		panic(err)
	}

	err = createEntity(c, ctx, "Price Now Otahuhu", "price_now_ota", "$")
	if err != nil {
		panic(err)
	}

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			wits, err := fetchWitsInfo()
			if err != nil {
				logError("Error fetching info: ", err)
			}
			for _, node := range wits.Charts.PricesLastFiveMinsMap.Data.Nodes {
				if node.Name == "Otahuhu" {
					err := updateEntity(c, ctx, "price_now_ota", fmt.Sprintf("%0.2f", node.Price/1000))
					if err != nil {
						logError("Error notifying home assistant: ", err)
					}
				}
			}
			continue
		case <-ctx.Done():
		}
		break
	}

	stop()
	<-c.Done() // Wait for clean shutdown (cancelling the context triggered the shutdown)
}

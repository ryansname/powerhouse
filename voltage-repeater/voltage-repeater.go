package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	ble "ryansname/powerhouse/voltage-repeater/victron_ble"

	"github.com/koestler/go-victron/bleparser"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/gookit/ini/v2"
)

var smartsolarStates = [...]string{
	"Not charging",
	"Fault",
	"Bulk Charging",
	"Absorption Charging",
	"Float Charging",
	"Manual Equalise",
	"Wake-Up",
	"Auto Equalise",
	"External Control",
	"Unavailable",
}

var msgId uint16 = 0

func makeClientConfig(config *ini.Ini, bleConfig *BleConfig, haOnline chan<- struct{}) (*autopaho.ClientConfig, error) {
	clientID := "voltage-repeater"

	rawHost := config.String("mqtt.host")
	u, err := url.Parse("mqtt://" + rawHost + ":1883")
	if err != nil {
		return nil, err
	}
	fmt.Println(u)

	cliCfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{u},
		ConnectUsername:               config.String("mqtt.username"),
		ConnectPassword:               []byte(config.String("mqtt.password")),
		KeepAlive:                     20,
		CleanStartOnInitialConnection: true,
		SessionExpiryInterval:         60,
		OnConnectionUp: func(cm *autopaho.ConnectionManager, connAck *paho.Connack) {
			fmt.Println("mqtt connection up")
			ctx := context.Background()
			if _, err := cm.Subscribe(ctx, &paho.Subscribe{
				Subscriptions: []paho.SubscribeOptions{
					{Topic: "homeassistant/status", QoS: 1},
				},
			}); err != nil {
				fmt.Printf("failed to subscribe to homeassistant/status: %s\n", err)
			}
			fmt.Println("re-registering HA entities")
			if err := setupHomeAssistant(cm, ctx, config, bleConfig); err != nil {
				fmt.Printf("failed to setup HA entities: %s\n", err)
			}
		},
		OnConnectError: func(err error) { fmt.Printf("error whilst attempting connection: %s\n", err) },
		ClientConfig: paho.ClientConfig{
			ClientID: clientID,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				func(pr paho.PublishReceived) (bool, error) {
					if pr.Packet.Topic == "homeassistant/status" {
						fmt.Printf("home assistant status: %s (retain: %t)\n", pr.Packet.Payload, pr.Packet.Retain)
						if string(pr.Packet.Payload) == "online" && !pr.Packet.Retain {
							select {
							case haOnline <- struct{}{}:
							default:
							}
						}
					}
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

func macToTopic(mac string) string {
	return strings.ReplaceAll(mac, ":", "")
}

func createEnumEntity(client *autopaho.ConnectionManager, ctx context.Context, entityName, entityClass, entityMeasure, deviceName, deviceMac, deviceModel, jsonKey, stateClass string, options []string) error {
	err := createEntity(client, ctx, entityName, entityClass, entityMeasure, deviceName, deviceMac, deviceModel, jsonKey, stateClass, 0)
	if err != nil {
		return err
	}

	// TODO: options should be sent seperately
	return nil
}

func createEntity(
	client *autopaho.ConnectionManager,
	ctx context.Context,
	entityName, entityClass, entityMeasure, deviceName, deviceMac, deviceModel, jsonKey, stateClass string,
	displayPrecision int,
) error {
	type Config struct {
		Name             string `json:"name,omitempty"`
		DeviceClass      string `json:"device_class"`
		StateTopic       string `json:"state_topic"`
		UnitOfMeasure    string `json:"unit_of_measurement,omitempty"`
		ValueTemplate    string `json:"value_template"`
		UniqueId         string `json:"unique_id"`
		ExpireAfter      uint   `json:"expire_after,omitempty"`
		StateClass       string `json:"state_class,omitempty"`
		DisplayPrecision int    `json:"suggested_display_precision,omitempty"`
		Device           struct {
			Identifiers  []string `json:"identifiers"`
			Name         string   `json:"name"`
			Manufacturer string   `json:"manufacturer,omitempty"`
			Model        string   `json:"model,omitempty"`
		} `json:"device"`
	}
	config := Config{}
	config.Name = entityName
	config.DeviceClass = entityClass
	config.StateTopic = "voltagerepeater/sensor/" + macToTopic(deviceMac) + "/state"
	config.UnitOfMeasure = entityMeasure
	config.ValueTemplate = "{{ value_json." + jsonKey + "}}"
	config.UniqueId = deviceMac + " " + entityName
	config.ExpireAfter = 60 * 2
	config.StateClass = stateClass
	config.DisplayPrecision = displayPrecision
	config.Device.Identifiers = []string{deviceMac, deviceName}
	config.Device.Name = deviceName
	config.Device.Manufacturer = "Victron"
	config.Device.Model = deviceModel

	msgId += 1
	publish := paho.Publish{}
	publish.PacketID = msgId
	publish.QoS = 2
	publish.Retain = true
	publish.Topic = "homeassistant/sensor/" + macToTopic(deviceMac) + "-" + jsonKey + "/config"

	payloadString, err := json.Marshal(config)
	if err != nil {
		return err
	}
	publish.Payload = []byte(payloadString)

	client.Publish(ctx, &publish)
	return nil
}

func updateEntity(client *autopaho.ConnectionManager, ctx context.Context, deviceMac string, data map[string]interface{}) error {
	payloadString, err := json.Marshal(data)
	if err != nil {
		return err
	}

	msgId += 1

	publish := paho.Publish{}
	publish.PacketID = msgId
	publish.QoS = 2
	publish.Topic = "voltagerepeater/sensor/" + macToTopic(deviceMac) + "/state"
	publish.Payload = []byte(payloadString)

	client.Publish(ctx, &publish)
	return nil
}

func logError(msg string, err error) {
	fmt.Fprint(os.Stderr, msg, err)
}

func setupHomeAssistant(client *autopaho.ConnectionManager, ctx context.Context, config *ini.Ini, bleConfig *BleConfig) error {
	if err := createEntity(client, ctx, "Voltage", "voltage", "V", "Powerhouse Battery", config.String("batterysense.mac"), "Smart Battery Sense", "voltage", "measurement", 2); err != nil {
		return err
	}

	if err := createEntity(client, ctx, "Temperature", "temperature", "°C", "Powerhouse Battery", config.String("batterysense.mac"), "Smart Battery Sense", "temperature", "measurement", 2); err != nil {
		return err
	}

	for _, dev := range bleConfig.devices[1:] {
		if err := createEntity(client, ctx, "Solar Power", "power", "W", dev.name, dev.mac, dev.model, "solar_power", "measurement", 0); err != nil {
			return err
		}

		if err := createEntity(client, ctx, "Solar Energy", "energy", "Wh", dev.name, dev.mac, dev.model, "energy_today", "total_increasing", 0); err != nil {
			return err
		}

		if err := createEnumEntity(client, ctx, "Charge State", "enum", "", dev.name, dev.mac, dev.model, "charge_state", "", smartsolarStates[:]); err != nil {
			return err
		}

		if err := createEntity(client, ctx, "Battery Voltage", "voltage", "V", dev.name, dev.mac, dev.model, "battery_voltage", "measurement", 2); err != nil {
			return err
		}

		if err := createEntity(client, ctx, "Battery Current", "current", "A", dev.name, dev.mac, dev.model, "battery_current", "measurement", 2); err != nil {
			return err
		}
	}

	return nil
}

type BleDevice struct {
	id      string
	name    string
	model   string
	mac     string
	key     []byte
	channel chan interface{}
}

func (d BleDevice) Name() string {
	return d.name
}
func (d BleDevice) MacAddress() string {
	return d.mac
}
func (d BleDevice) EncryptionKey() []byte {
	return d.key
}
func (d BleDevice) MessageChannel() chan<- interface{} {
	return d.channel
}

type BleConfig struct {
	name    string
	devices []BleDevice
}

func ConvertToBleConfig(config *ini.Ini) (*BleConfig, error) {
	devices := make([]BleDevice, 0)
	deviceIds := [...]string{"batterysense", "solar3", "solar4", "solar5"}

	for _, deviceId := range deviceIds {
		keyString := config.String(deviceId + ".key")
		keyBytes, err := hex.DecodeString(keyString)
		if err != nil {
			return nil, err
		}

		device := BleDevice{
			deviceId,
			config.String(deviceId + ".name"),
			config.String(deviceId + ".device"),
			config.String(deviceId + ".mac"),
			keyBytes,
			make(chan interface{}, 32),
		}
		if device.mac == "" {
			return nil, fmt.Errorf("Device %s has no value for 'mac', expected device mac address", deviceId)
		}
		if len(device.key) == 0 {
			return nil, fmt.Errorf("Device %s has no value for 'mac', expected device mac address", deviceId)
		}
		devices = append(devices, device)
	}

	return &BleConfig{
		"Voltage Repeater",
		devices,
	}, nil
}

func (b BleConfig) Name() string {
	return b.name
}
func (b BleConfig) LogDebug() bool {
	return false
}
func (b BleConfig) Devices() []ble.DeviceConfig {
	devices := make([]ble.DeviceConfig, len(b.devices))
	for i, device := range b.devices {
		devices[i] = device
	}
	return devices
}

func main() {
	// App will run until cancelled by user (e.g. ctrl-c)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	config := ini.New()
	err := config.LoadExists("/etc/voltage-repeater.ini", "voltage-repeater.ini")
	if err != nil {
		panic(err)
	}

	bleConfig, err := ConvertToBleConfig(config)
	if err != nil {
		panic(err)
	}

	haOnline := make(chan struct{}, 1)

	clientConfig, err := makeClientConfig(config, bleConfig, haOnline)
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

	victronBle, err := ble.New(bleConfig)
	if err != nil {
		panic(err)
	}
	defer victronBle.Shutdown()

	ticker := time.NewTicker(time.Second)
outer:
	for {
		select {
		case <-ctx.Done():
			break outer
		case <-haOnline:
			fmt.Println("Home Assistant restarted, re-registering entities")
			if err := setupHomeAssistant(c, ctx, config, bleConfig); err != nil {
				fmt.Printf("failed to re-register HA entities: %s\n", err)
			}
			continue
		case <-ticker.C:
		}

		for _, dev := range bleConfig.devices {
			var msg interface{}
		inner:
			for {
				select {
				case msg = <-dev.channel:
					// Got a message, process it
				default:
					// Try the next device
					break inner
				}
			}

			if msg == nil {
				continue
			}

			switch m := msg.(type) {
			case bleparser.BatteryMonitorRecord:
				data := make(map[string]any)
				data["temperature"] = m.Temperature
				data["voltage"] = m.BatteryVoltage
				updateEntity(c, ctx, dev.mac, data)
			case bleparser.SolarChargerRecord:
				data := make(map[string]any)
				data["solar_power"] = m.PvPower
				data["energy_today"] = m.YieldToday
				data["charge_state"] = m.DeviceState.String()
				data["battery_voltage"] = m.BatteryVoltage
				data["battery_current"] = m.BatteryCurrent
				updateEntity(c, ctx, dev.mac, data)
			}
		}
	}

	stop()
	<-c.Done() // Wait for clean shutdown (cancelling the context triggered the shutdown)
}

package control

import (
	"context"
	"fmt"
	"maps"
	"math/rand"
	"strconv"
	"time"

	"github.com/gookit/ini/v2"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type obj = map[string]any

func startWebsocket(ctx context.Context, websocketOutput chan<- obj, websocketSend <-chan obj) error {
	c, _, err := websocket.Dial(ctx, "ws://homeassistant.lan:8123/api/websocket", &websocket.DialOptions{})
	if err != nil {
		return err
	}

	socketContext, socketCancel := context.WithCancel(ctx)
	_ = context.AfterFunc(socketContext, func() {
		c.CloseNow()
		fmt.Println("Socket disconnected")

		// If the context is still active, try restarting
		if context.Cause(ctx) == nil {
			go func() {
				fmt.Println("Restarting websocket in 5 seconds")
				time.Sleep(time.Second * 5)
				go startWebsocket(ctx, websocketOutput, websocketSend)
			}()
		}
	})
	fmt.Println("Socket connected")

	go func() {
		defer socketCancel()
		defer fmt.Println("Websocket reader closed")
		for context.Cause(socketContext) == nil {
			result := map[string]interface{}{}
			// readCtx, readCancel := context.WithTimeout(socketContext, 3*time.Second)
			readCtx, readCancel := context.WithCancel(socketContext)
			defer readCancel()

			if err := wsjson.Read(readCtx, c, &result); err != nil {
				fmt.Printf("Error reading: %v\n", err)
				break
			} else {
				websocketOutput <- result
			}
		}
	}()
	go func() {
		defer socketCancel()
		defer fmt.Println("Websocket writer closed")
		nextId := 1
		for {
			select {
			case <-socketContext.Done():
			case msg := <-websocketSend:
				fmt.Println("Sending", msg)
				if msg["type"] != "auth" {
					msg["id"] = nextId
					nextId += 1
				}

				err := wsjson.Write(socketContext, c, msg)
				if err != nil {
					fmt.Printf("Error writing: %v\n", err)
				} else {
					continue
				}
			}
			break
		}
	}()
	return nil
}

const POWERHOUSE_CONTROL_MANUAL = "input_boolean.powerhouse_control_manual"
const SOLAR_GENERATION_AVERAGE = "sensor.powerhouse_solar_charger_solar_power_average"
const SOLAR_GENERATION = "sensor.powerhouse_solar_charger_solar_power"
const LOAD_USAGE = "sensor.powerwall_load_now"
const LOAD_USAGE_AVERAGE = "sensor.powerwall_load_now_average"
const CHARGER_STATE = "sensor.powerhouse_solar_charger_charge_state"
const CHARGER_VOLTAGE = "sensor.powerhouse_solar_charger_battery_voltage"

func inverterPowerKey(index int) string {
	return fmt.Sprintf("sensor.powerhouse_inverter_%d_switch_0_power", index+1)
}
func inverterStateKey(index int) string {
	return fmt.Sprintf("switch.powerhouse_inverter_%d_switch_0", index+1)
}

func sendSubscriptions(c chan<- obj) {
	entityIds := []string{}
	entityIds = append(entityIds, POWERHOUSE_CONTROL_MANUAL)
	entityIds = append(entityIds, SOLAR_GENERATION_AVERAGE)
	entityIds = append(entityIds, SOLAR_GENERATION)
	entityIds = append(entityIds, LOAD_USAGE)
	entityIds = append(entityIds, CHARGER_STATE)
	entityIds = append(entityIds, CHARGER_VOLTAGE)
	for i := range 9 {
		entityIds = append(entityIds, inverterStateKey(i))
		entityIds = append(entityIds, inverterPowerKey(i))
	}

	c <- obj{"id": 1, "type": "subscribe_entities", "entity_ids": entityIds}
}

func updateState(event obj, state map[string]interface{}) {
	if event["a"] != nil {
		additions := event["a"].(obj)
		for name, entity := range additions {
			state[name] = entity.(obj)["s"]
		}
	}
	if event["c"] != nil {
		changes := event["c"].(obj)
		for name, entity := range changes {
			newState := entity.(obj)["+"].(obj)["s"]
			if newState != nil {
				state[name] = newState
			}
		}
	}
}

func nextId(state map[string]interface{}) (id int) {
	id = state["nextId"].(int)
	state["waitId"] = id
	state["nextId"] = id + 1
	return
}

func waitId(state map[string]interface{}) int {
	return state["waitId"].(int)
}

func haInt(value interface{}) int {
	if v, ok := value.(int); ok {
		return v
	}

	if f, ok := value.(float64); ok {
		return int(f)
	}

	if value == "Unavailable" {
		return -1
	}

	res, err := strconv.Atoi(value.(string))
	if err != nil {
		fmt.Printf("Failed to convert %s to int: %v\n", value, err)
		return -2
	}
	return res
}

func haF64(value interface{}) float64 {
	if value == nil {
		return -3.0
	}
	if f, ok := value.(float64); ok {
		return f
	}
	if value == "Unavailable" {
		return -1.0
	}
	res, err := strconv.ParseFloat(value.(string), 64)
	if err != nil {
		fmt.Printf("Failed to convert %s to float: %v\n", value, err)
		return -2
	}
	return res
}

func haBool(value interface{}, unavailableResult bool) bool {
	if b, ok := value.(bool); ok {
		return b
	}

	switch value {
	case "Unavailable":
		return unavailableResult
	case "on":
		return true
	case "off":
		return false
	}

	res, err := strconv.ParseBool(value.(string))
	if err != nil {
		fmt.Printf("Failed to convert %s to bool: %v\n", value, err)
		return false
	}
	return res
}

type MapDetails struct {
	state    map[string]interface{}
	sendChan chan<- obj
}

func (d MapDetails) AverageSolarGeneration() float64 {
	return haF64(d.state[SOLAR_GENERATION_AVERAGE])
}
func (d MapDetails) CurrentSolarGeneration() float64 {
	return haF64(d.state[SOLAR_GENERATION])
}
func (d MapDetails) PowerPerInverter() float64 {
	return 250
}
func (d MapDetails) ExpectedInvertingPower() float64 {
	count := 0.0
	for i := range 9 {
		if haF64(d.state[inverterPowerKey(i)]) < 0 {
			count += 1
		} else if haBool(d.state[inverterStateKey(i)], false) {
			count += 1
		}
	}
	return count * d.PowerPerInverter()
}

func (d MapDetails) EnableInverters(count int) {
	invertersPoweredOff := make([]int, 0)
	for i := range 9 {
		if !haBool(d.state[inverterStateKey(i)], true) {
			invertersPoweredOff = append(invertersPoweredOff, i)
		}
	}
	for range count {
		fmt.Println("Enable inverter")
		randIdx := rand.Intn(len(invertersPoweredOff))

		inverterId := invertersPoweredOff[randIdx]
		entityId := inverterStateKey(inverterId)
		msg := obj{
			"type":    "call_service",
			"domain":  "switch",
			"service": "turn_on",
			"target": obj{
				"entity_id": []string{entityId},
			},
		}
		d.sendChan <- msg
		_ = msg

		invertersPoweredOff[randIdx] = invertersPoweredOff[len(invertersPoweredOff)-1]
		invertersPoweredOff = invertersPoweredOff[:len(invertersPoweredOff)-1]
	}
}
func (d MapDetails) DisableInverters(count int) {
	invertersGenerating := make([]int, 0)
	invertersPoweredIdle := make([]int, 0)

	for i := range 9 {
		if haF64(d.state[inverterPowerKey(i)]) < 0 {
			invertersGenerating = append(invertersGenerating, i)
		} else if haBool(d.state[inverterStateKey(i)], false) {
			invertersPoweredIdle = append(invertersPoweredIdle, i)
		}
	}

	countToDisable := count
	for _, list := range [][]int{invertersPoweredIdle, invertersGenerating} {
		for len(list) > 0 && countToDisable > 0 {
			countToDisable -= 1

			fmt.Println("Disable inverter")
			randIdx := rand.Intn(len(list))

			inverterId := list[randIdx]
			entityId := inverterStateKey(inverterId)
			msg := obj{
				"type":    "call_service",
				"domain":  "switch",
				"service": "turn_off",
				"target": obj{
					"entity_id": []string{entityId},
				},
			}
			d.sendChan <- msg
			_ = msg

			list[randIdx] = list[len(list)-1]
			list = list[:len(list)-1]
		}
	}
}

type kv struct {
	k string
	v interface{}
}

type average struct {
	inputKey         string
	outputKey        string
	duration         time.Duration
	records          []float64
	average          float64
	idx              int
	lastEmittedValue float64
}

func calculateAverages(mapIn <-chan map[string]interface{}) <-chan kv {
	averages := []*average{
		{inputKey: LOAD_USAGE, outputKey: LOAD_USAGE_AVERAGE, duration: time.Minute * 1},
		{inputKey: SOLAR_GENERATION, outputKey: SOLAR_GENERATION_AVERAGE, duration: time.Minute * 15},
		{inputKey: CHARGER_VOLTAGE, outputKey: CHARGER_VOLTAGE + "_1", duration: time.Minute * 1},
	}
	results := make(chan kv, 128)

	go func() {
		activeMap := <-mapIn
		for _, avg := range averages {
			recordCount := avg.duration / time.Second

			avg.average = haF64(activeMap[avg.inputKey])
			avg.records = make([]float64, recordCount)
			for i := range avg.records {
				avg.records[i] = avg.average
			}
			results <- kv{avg.outputKey, avg.average}
			avg.lastEmittedValue = avg.average
		}

		ticker := time.NewTicker(time.Second)

		for {
			select {
			case m := <-mapIn:
				activeMap = m
			case <-ticker.C:
				for _, avg := range averages {
					s := len(avg.records)
					newValue := haF64(activeMap[avg.inputKey])
					avg.average += (newValue - avg.records[avg.idx]) / float64(s)
					avg.records[avg.idx] = newValue

					avg.idx = (avg.idx + 1) % s

					if avg.lastEmittedValue != avg.average {
						results <- kv{avg.outputKey, avg.average}
						avg.lastEmittedValue = avg.average
					}
				}
			}
		}
	}()

	return results
}

func Run() {
	config := ini.New()
	err := config.LoadExists("/etc/battery-control.ini", "battery-control.ini")
	if err != nil {
		panic(err)
	}
	accessToken := config.String("homeassistant.access_token")

	_ = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	// ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	recvChan := make(chan obj)
	sendChan := make(chan obj)
	err = startWebsocket(ctx, recvChan, sendChan)
	if err != nil {
		panic(err)
	}

	detailsChan := make(chan Details)
	state := map[string]interface{}{}

	stateChan := make(chan map[string]interface{}, 128)
	averagesChan := calculateAverages(stateChan)

	go DumpPower(detailsChan)
	for {
		select {
		case msg := <-recvChan:
			if msg["type"] == "auth_required" {
				sendChan <- obj{"type": "auth", "access_token": accessToken}
				sendSubscriptions(sendChan)
			} else if msg["type"] == "event" {
				event := msg["event"].(obj)
				// fmt.Println("Event:", event)
				updateState(event, state)
				stateRead := maps.Clone(state)
				stateChan <- stateRead

				if !haBool(state[POWERHOUSE_CONTROL_MANUAL], false) {
					detailsChan <- MapDetails{
						stateRead,
						sendChan,
					}
				}

			} else if msg["type"] == "result" {
				if !haBool(msg["success"], false) {
					fmt.Println("Got failure result:", msg)
				}
			} else {
				fmt.Println("Unknown message: ", msg)
			}
		case average := <-averagesChan:
			// fmt.Println(average)
			state[average.k] = average.v
			stateRead := maps.Clone(state)
			if !haBool(state[POWERHOUSE_CONTROL_MANUAL], false) {
				detailsChan <- MapDetails{
					stateRead,
					sendChan,
				}
				break
			}
		}
	}
}

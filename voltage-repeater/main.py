#!/usr/bin/env python

import asyncio
import configparser
import inspect
import json
import logging
import paho.mqtt.client as mqtt
import victron_ble.scanner as scanner
import victron_ble.devices as devices

from bleak.backends.device import BLEDevice
from enum import Enum

# https://github.com/keshavdv/victron-ble/blob/d6d67d885b0eb515c32fadcde01475beb370a48b/victron_ble/scanner.py#L52
class DeviceDataEncoder(json.JSONEncoder):
    def default(self, obj):
        if issubclass(obj.__class__, devices.DeviceData):
            data = {}
            for name, method in inspect.getmembers(obj, predicate=inspect.ismethod):
                if name.startswith("get_"):
                    value = method()
                    if isinstance(value, Enum):
                        value = value.name.lower()
                    if value is not None:
                        data[name[4:]] = value
            return data

class BatteryScanner(scanner.Scanner):
    def __init__(self, client, keys) -> None:
        super().__init__(keys)
        self.client = client

    # https://github.com/keshavdv/victron-ble/blob/d6d67d885b0eb515c32fadcde01475beb370a48b/victron_ble/scanner.py#L96
    def callback(self, ble_device: BLEDevice, raw_data: bytes):
        try:
            device = self.get_device(ble_device, raw_data)
        except AdvertisementKeyMissingError:
            return
        except UnknownDeviceError as e:
            logger.error(e)
            return
        parsed = device.parse(raw_data)
        logging.debug(json.dumps(parsed, cls=DeviceDataEncoder))

        state = {
            "temperature": parsed.get_temperature(),
            "voltage": parsed.get_voltage(),
        }
        msg = self.client.publish("homeassistant/sensor/powerhouse-battery/state", json.dumps(state), qos=2)
        msg.wait_for_publish()

def on_connect(client, userdata, flags, reason_code, properties):
    print(f"Connected with result code {reason_code}")

def on_message(client, userdata, msg):
    print(msg.topic+" "+str(msg.payload))

def create_entity(client, device_id, name, measure):
    config = {
       "device_class":name,
       "state_topic":"homeassistant/sensor/powerhouse-battery/state",
       "unit_of_measurement":measure,
       "value_template":"{{ value_json." + name + " }}",
       "unique_id": device_id + "-" + name, 
       "expire_after": 120,
       "device":{
          "identifiers":[
              device_id
          ],
          "name":"Powerhouse Battery",
          "manufacturer": "Victron",
          "model": "Smart Battery Sense",
       }
    }

    msg = client.publish("homeassistant/sensor/powerhouse-battery-" + name + "/config", json.dumps(config), qos=2)
    msg.wait_for_publish()
    
def init_client(config):
    client = mqtt.Client(mqtt.CallbackAPIVersion.VERSION2)
    client.on_message = on_message
    client.on_connect = on_connect

    client.username = config.get("username")
    client.password = config.get("password")
    client.connect(config.get("host"), config.getint("port", 1883))
    # client.enable_logger()

    client.loop_start()
    return client

def init_scanner(config, client):
    scanner = BatteryScanner(client, {config.get("mac"): config.get("key")})
    loop = asyncio.get_event_loop()
    asyncio.ensure_future(scanner.start())
    return loop

def main():
    config = configparser.ConfigParser()
    config.read(["/etc/voltage-repeater.ini", "voltage-repeater.ini"])
    
    client = init_client(config["mqtt"])
    scanner = init_scanner(config["batterysense"], client)

    batterysense_mac = config["batterysense"]["mac"]
    create_entity(client, batterysense_mac, "voltage", "V")
    create_entity(client, batterysense_mac, "temperature", "°C")

    scanner.run_forever()
    
    client.disconnect()
    client.loop_stop()


if __name__ == "__main__":
    # logging.basicConfig(level=logging.DEBUG)
    main()


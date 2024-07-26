package ble

// This file is uses some code from the given URL licensed under the MIT license below
// https://github.com/koestler/go-victron/blob/88555ebf8af9f7963637379293b9acedbdc33761/ble/ble.go
// MIT License

// Copyright (c) 2023 Lorenz

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"log"

	"github.com/koestler/go-victron/bleparser"
	"github.com/koestler/go-victron/veproduct"
	"tinygo.org/x/bluetooth"
)

const VictronManufacturerId = 0x2e1

const TestRecord = 0x00
const SolarCharger = 0x01
const BatteryMonitor = 0x02
const Inverter = 0x03
const DcdcConverter = 0x04
const SmartLithium = 0x05
const InverterRs = 0x06
const GxDevice = 0x07
const AcCharger = 0x08
const SmartBatteryProtect = 0x09
const LynxSmartBms = 0x0A
const MultiRs = 0x0B
const VeBus = 0x0C
const DcEnergyMeter = 0x0D

type Config interface {
	Name() string
	LogDebug() bool
	Devices() []DeviceConfig
}

type DeviceConfig interface {
	Name() string
	MacAddress() string // AA:BB:CC:DD:EE:FF
	EncryptionKey() []byte
	MessageChannel() chan<- interface{}
}

type BleStruct struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc
}

func New(cfg Config) (*BleStruct, error) {
	ctx, cancel := context.WithCancel(context.Background())

	ble := &BleStruct{
		cfg:    cfg,
		ctx:    ctx,
		cancel: cancel,
	}

	adapter := bluetooth.DefaultAdapter

	err := adapter.Enable()
	if err != nil {
		return nil, err
	}

	go func() {
		if cfg.LogDebug() {
			log.Printf("ble[%s]: start listening", ble.Name())
		}

		// scanCtx, scanCancel := context.WithTimeout(ctx, time.Minute)
		scanCtx, scanCancel := context.WithCancel(ctx)
		defer scanCancel()
		go func() {
			<-scanCtx.Done()
			log.Printf("Stop scanning")
			adapter.StopScan()
		}()

		err = adapter.Scan(func(adapter *bluetooth.Adapter, scanResult bluetooth.ScanResult) {
			for _, device := range cfg.Devices() {
				if scanResult.Address.String() == device.MacAddress() {
					if cfg.LogDebug() {
						log.Printf("ble[%s]: found device with path %s: %s", cfg.Name(), scanResult.LocalName(), scanResult.Address)
					}
					ble.handleNewManufacturerData(device, scanResult.AdvertisementPayload.ManufacturerData()[0].Data)
					break
				}
			}
		})
		log.Printf("Scanning stopped")

		if err != nil {
			log.Printf("ble[%s]: error while scanning: %s", ble.Name(), err)
		}
	}()

	return ble, nil
}

func (ble *BleStruct) handleNewManufacturerData(deviceConfig DeviceConfig, rawBytes []uint8) {
	if ble.cfg.LogDebug() {
		log.Printf("ble[%s]->%s: handle len=%d, rawBytes=%x",
			ble.cfg.Name(), deviceConfig.Name(), len(rawBytes), rawBytes,
		)
	}

	if len(rawBytes) < 9 {
		log.Printf("ble[%s]->%s: len(rawBytes) is to low",
			ble.cfg.Name(), deviceConfig.Name(),
		)
		return
	}

	// map rawBytes:
	// 00 - 01 : prefix
	// 02 - 03 : product id
	// 04 - 04 : record type
	// 05 - 06 : Nonce/Data counter in LSB order
	// 07 - 07 : first byte of encryption key
	// 08 -    : encrypted data

	prefix := rawBytes[0:2]
	productId := binary.LittleEndian.Uint16(rawBytes[2:4])
	product := veproduct.Product(productId)
	recordType := rawBytes[4]
	nonce := rawBytes[5:7] // used ad iv for encryption; is only 16 bits
	iv := binary.LittleEndian.Uint16(nonce)

	firstByteOfEncryptionKey := rawBytes[7]
	encryptedBytes := rawBytes[8:]

	if ble.cfg.LogDebug() {
		log.Printf("ble[%s]->%s: prefix=%x productId=%x productString=%s recordType=%x, nonce=%d, firstByteOfEncryptionKey=%x",
			ble.cfg.Name(), deviceConfig.Name(), prefix, productId, product.String(), recordType, nonce, firstByteOfEncryptionKey,
		)
	}

	if ble.cfg.LogDebug() {
		log.Printf("ble[%s]->%s: encryptedBytes=%x, len=%d",
			ble.cfg.Name(), deviceConfig.Name(), encryptedBytes, len(encryptedBytes),
		)
	}

	// decrypt rawBytes using aes-ctr algorithm
	// encryption key of config is fixed to 32 hex chars, so 16 bytes, so 128-bit AES is used here
	encryptionKey := deviceConfig.EncryptionKey()
	if len(encryptionKey) != 16 {
		log.Printf("Expected encryption key to be 16 bytes, got %d", len(encryptionKey))
		return
	}

	block, err := aes.NewCipher(deviceConfig.EncryptionKey())
	if err != nil {
		log.Printf("ble[%s]->%s: cannot create aes cipher: %s",
			ble.cfg.Name(), deviceConfig.Name(), err,
		)
		return
	}

	paddedEncryptedBytes := PKCS7Padding(encryptedBytes, block.BlockSize())

	if ble.cfg.LogDebug() {
		log.Printf("ble[%s]->%s: paddedEncryptedBytes=%x, len=%d",
			ble.cfg.Name(), deviceConfig.Name(), paddedEncryptedBytes, len(paddedEncryptedBytes),
		)
	}

	decryptedBytes := make([]byte, len(paddedEncryptedBytes))

	// iv needs to be 16 bytes for 128-bit AES, use nonce and pad with 0
	ivBytes := make([]byte, 16)
	binary.LittleEndian.PutUint16(ivBytes, iv)

	if ble.cfg.LogDebug() {
		log.Printf("ble[%s]->%s: iv=%d ivBytes=%x, len=%d",
			ble.cfg.Name(), deviceConfig.Name(),
			iv, ivBytes, len(ivBytes),
		)
	}

	ctrStream := cipher.NewCTR(block, ivBytes)
	ctrStream.XORKeyStream(decryptedBytes, paddedEncryptedBytes)

	if ble.cfg.LogDebug() {
		log.Printf("ble[%s]->%s: decryptedBytes=%x, len=%d",
			ble.cfg.Name(), deviceConfig.Name(), decryptedBytes, len(decryptedBytes),
		)
	}

	// handle decryptedBytes
	switch recordType {
	case SolarCharger:
		// solar charger
		record, err := bleparser.DecodeSolarChargeRecord(decryptedBytes)
		if err != nil {
			log.Printf("ble[%s]->%s: cannot decode solar charger record: %s",
				ble.cfg.Name(), deviceConfig.Name(), err,
			)
			return
		}
		if ble.cfg.LogDebug() {
			log.Printf("ble[%s]->%s: solar charger record=%#v", ble.cfg.Name(), deviceConfig.Name(), record)
		}
		deviceConfig.MessageChannel() <- record

	case BatteryMonitor:
		record, err := bleparser.DecodeBatteryMonitorRecord(decryptedBytes)
		if err != nil {
			log.Printf("ble[%s]->%s: cannot decode battery monitor record: %s",
				ble.cfg.Name(), deviceConfig.Name(), err,
			)
			return
		}
		if ble.cfg.LogDebug() {
			log.Printf("ble[%s]->%s: battery monitor record=%#v", ble.cfg.Name(), deviceConfig.Name(), record)
		}
		deviceConfig.MessageChannel() <- record
	}
}

func PKCS7Padding(ciphertext []byte, blocksize int) []byte {
	padding := blocksize - len(ciphertext)%blocksize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(ciphertext, padtext...)
}

func (ble *BleStruct) getDeviceConfig(bluezAddr string) DeviceConfig {
	for _, d := range ble.cfg.Devices() {
		if d.MacAddress() == bluezAddr {
			return d
		}
	}
	return nil
}

func (ble *BleStruct) Name() string {
	return ble.cfg.Name()
}

func (ble *BleStruct) Shutdown() {
	ble.cancel()
}

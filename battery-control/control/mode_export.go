package control

import "time"
import "fmt"

type Details interface {
	ChargerState() string
	AverageSolarGeneration() float64
	CurrentSolarGeneration() float64
	AverageLoadPower() float64
	CurrentLoadPower() float64
	PowerPerInverter() float64
	EnabledInverters() int64

	SetDebug(debug string)
	SetInverterCount(count int64)
	EnableInverters(count int64)
	DisableInverters(count int64)
}

func DumpPower(detailsIn <-chan Details) {
	time.Sleep(time.Second * 5)

	for details := range detailsIn {
		generation := details.AverageSolarGeneration()
		powerPerInverter := details.PowerPerInverter()
		expectedInvertingPower := float64(details.EnabledInverters()) * powerPerInverter

		details.SetDebug(fmt.Sprintf("Solar Generation %0.0f\nExpected Inverting Power: %0.0f", generation, expectedInvertingPower))
		if expectedInvertingPower+powerPerInverter+powerPerInverter/2 < generation {
			details.EnableInverters(1)
		} else if expectedInvertingPower-powerPerInverter/2 > generation {
			details.DisableInverters(1)
		}

		sleep := time.NewTicker(time.Second * 5)
		for {
			select {
			case <-detailsIn:
				continue
			case <-sleep.C:
			}
			break
		}
	}
}

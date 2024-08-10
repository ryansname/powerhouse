package control

import "time"
import "fmt"

type Details interface {
	AverageSolarGeneration() float64
	CurrentSolarGeneration() float64
	PowerPerInverter() float64
	ExpectedInvertingPower() float64

	EnableInverters(count int)
	DisableInverters(count int)
}

func DumpPower(detailsIn <-chan Details) {
	time.Sleep(time.Second * 5)

	for details := range detailsIn {
		generation := details.AverageSolarGeneration()
		PowerPerInverter := details.PowerPerInverter()
		expectedInvertingPower := details.ExpectedInvertingPower()

		fmt.Println("Solar generation:", generation, "Expected Inverting Power", expectedInvertingPower)
		if expectedInvertingPower+PowerPerInverter+PowerPerInverter/2 < generation {
			details.EnableInverters(1)
		} else if expectedInvertingPower-PowerPerInverter/2 > generation {
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
